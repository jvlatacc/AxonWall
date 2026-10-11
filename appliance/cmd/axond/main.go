// Command axond serves the AxonWall REST API over TLS with token auth.
//
// Security defaults: HTTPS only, bearer token required for the config
// endpoints, bound to the LAN zone's address per the config store. The
// daemon refuses to start without a token source — there is no insecure
// mode. The committed config is rendered and applied at boot; config
// changes flow through the apply pipeline (validate → stage known-good →
// atomic apply → reload → health check → commit → confirm-or-rollback).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/apply"
	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/health"
	"github.com/jvlatacc/AxonWall/appliance/internal/rollback"
	"github.com/jvlatacc/AxonWall/appliance/internal/services"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

func main() {
	configDir := flag.String("config-dir", store.DefaultDir, "config store directory")
	port := flag.String("port", "443", "API port")
	tokenFile := flag.String("token-file", "", "file containing the API bearer token")
	devToken := flag.String("dev-token", "", "API bearer token (development only)")
	certFile := flag.String("tls-cert", "", "TLS certificate path")
	keyFile := flag.String("tls-key", "", "TLS key path")
	uiDir := flag.String("ui-dir", "", "directory of the built web console bundle, served on the API listener")
	skipApply := flag.Bool("skip-apply", false, "development only: skip rendering and applying the firewall at boot")
	flag.Parse()

	st, cfg, err := openStore(*configDir)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}
	apiToken, err := resolveToken(*devToken, *tokenFile)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}

	ln, err := lanListener(cfg, *port)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}

	pipeline := buildPipeline(apply.NewNftApplier(), st, ln.Addr().String())
	srv := NewServer(st, apiToken, pipeline)
	if *tokenFile != "" {
		// Token rotations persist to the boot token file so a rotated
		// admin token survives reboot.
		srv.SetTokenFile(*tokenFile)
	}
	if *uiDir != "" {
		srv.SetUIDir(*uiDir)
	}

	tlsConf, err := tlsConfig(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}
	ln = tls.NewListener(ln, tlsConf)

	// Serve the API before the boot apply: the apply's health check proves
	// management reachability by completing a TLS handshake against THIS
	// listener, and nothing answers that handshake until Serve is accepting
	// connections. Serving first also means the API is up by the time the
	// firewall-active marker (below) can fire.
	server := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	if !*skipApply {
		// Boot apply: the committed config must be enforcing before the API
		// accepts requests. Failure is fatal — an appliance that cannot load
		// its own firewall policy must stop, not serve unenforced.
		if err := pipeline.ApplyContext(context.Background(), cfg); err != nil {
			log.Fatalf("axond: apply committed config at boot: %v", err)
		}
		log.Printf("axond: firewall active (rendered from the committed config)")
		emitFirewallActiveMarker()
	} else {
		log.Printf("axond: --skip-apply set: firewall NOT applied (development only)")
	}

	// URL-table alias feeds refresh on their own cadence, outside the
	// config-apply path (scoped set updates; keep-last-good on failure).
	startAliasRefresher(context.Background(), st, apply.NewNftApplier())

	if host := lanBindHost(cfg); host == "" {
		log.Printf("axond: lan zone has no static address yet; listening on all interfaces")
	}
	if *uiDir != "" {
		log.Printf("axond: serving web console from %s on the API listener", *uiDir)
	}
	log.Printf("axond: serving API on https://%s", ln.Addr())

	// A returning Serve is always fatal: a healthy appliance daemon never
	// stops serving.
	log.Fatalf("axond: API server exited: %v", <-serveErr)
}

// buildPipeline assembles the apply pipeline: the nftables applier, the
// confirm-or-rollback timer manager, the service reloader (install rendered
// daemon configs + reload services), and the post-apply health check
// against the API listener axond is about to serve.
func buildPipeline(applier *apply.NftApplier, st *store.Store, apiAddr string) *apply.Pipeline {
	checker := &health.Checker{
		APIAddr: apiAddr,
		Timeout: 5 * time.Second,
		// Probe completes a TLS handshake instead of a bare TCP connect:
		// an aborted handshake logs noise in the HTTP server and proves
		// less. The probe checks liveness of our own listener, so peer
		// verification is intentionally off.
		Probe: func(ctx context.Context, addr string, timeout time.Duration) error {
			d := &tls.Dialer{
				NetDialer: &net.Dialer{Timeout: timeout},
				//nolint:gosec // liveness probe of our own listener; no peer verification needed
				Config: &tls.Config{InsecureSkipVerify: true},
			}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
	}
	pipeline := apply.NewPipeline(applier, rollback.NewManager(rollback.DefaultRestoreTimeout), checker, st)
	pipeline.Reload = services.NewReloader().Sync
	return pipeline
}

// openStore opens the config store, creating it with a first-boot default
// configuration when the directory has no axonwall.yaml yet.
func openStore(dir string) (*store.Store, *config.Config, error) {
	if _, err := os.Stat(store.Join(dir)); errors.Is(err, os.ErrNotExist) {
		st, err := store.Init(dir, config.Default())
		if err != nil {
			return nil, nil, fmt.Errorf("init store: %w", err)
		}
		cfg, _, err := st.Load()
		return st, cfg, err
	} else if err != nil {
		return nil, nil, fmt.Errorf("stat config store: %w", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	cfg, _, err := st.Load()
	if err != nil {
		return nil, nil, err
	}
	return st, cfg, nil
}

// resolveToken finds the API token: an explicit dev flag or a token file.
// Empty is fatal — the daemon must not serve unauthenticated.
func resolveToken(devToken, tokenFile string) (string, error) {
	if devToken != "" {
		return devToken, nil
	}
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read token file: %w", err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("token file %s is empty", tokenFile)
		}
		return token, nil
	}
	return "", fmt.Errorf("no API token: pass --token-file (or --dev-token in dev)")
}

// tlsConfig builds the server TLS policy, generating a bootstrap
// certificate when no cert/key files are provided.
func tlsConfig(certFile, keyFile string) (*tls.Config, error) {
	conf := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("--tls-cert and --tls-key must be given together")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS keypair: %w", err)
		}
		conf.Certificates = []tls.Certificate{cert}
		return conf, nil
	}
	cert, err := selfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("generate bootstrap certificate: %w", err)
	}
	conf.Certificates = []tls.Certificate{cert}
	return conf, nil
}
