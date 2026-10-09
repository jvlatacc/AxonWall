// Command axond serves the AxonWall REST API over TLS with token auth.
//
// Security defaults: HTTPS only, bearer token required for the config
// endpoints, bound to the LAN zone's address per the config store. The
// daemon refuses to start without a token source — there is no insecure
// mode.
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

func main() {
	configDir := flag.String("config-dir", store.DefaultDir, "config store directory")
	port := flag.String("port", "443", "API port")
	tokenFile := flag.String("token-file", "", "file containing the API bearer token")
	devToken := flag.String("dev-token", "", "API bearer token (development only)")
	certFile := flag.String("tls-cert", "", "TLS certificate path")
	keyFile := flag.String("tls-key", "", "TLS key path")
	flag.Parse()

	st, cfg, err := openStore(*configDir)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}
	apiToken, err := resolveToken(*devToken, *tokenFile)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}

	srv := NewServer(st, apiToken, nil)
	tlsConf, err := tlsConfig(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}
	ln, err := lanListener(cfg, *port)
	if err != nil {
		log.Fatalf("axond: %v", err)
	}
	ln = tls.NewListener(ln, tlsConf)

	if host := lanBindHost(cfg); host == "" {
		log.Printf("axond: lan zone has no static address yet; listening on all interfaces")
	}
	log.Printf("axond: serving API on https://%s", ln.Addr())
	server := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.Serve(ln))
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
