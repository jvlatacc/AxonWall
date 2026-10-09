// Command ax is the AxonWall CLI. It talks to a running axond over its
// REST API — on the appliance itself (local axond) or over the network
// (remote axond); the only difference is the URL. Config changes always
// flow through axond's apply pipeline; ax never edits daemon configs.
//
//	ax config get
//	ax config get --url https://192.168.1.1
//	ax config put -f new.yaml -m "add wireguard peer"
//
// Authentication uses --token or the AX_TOKEN environment variable.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "ax: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `ax — AxonWall CLI

Usage:
  ax config get
  ax config put -f <file> [-m <message>]

Flags:
  --url        base URL of axond (default: https://127.0.0.1)
  --token      API bearer token (falls back to $AX_TOKEN)
  --insecure   skip TLS verification (bootstrap self-signed certificates)
`)
}

// client issues authenticated requests against axond.
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

// addFlags registers the connection flags on fs and returns a constructor
// for the client once flags are parsed.
func addFlags(fs *flag.FlagSet) func() (*client, error) {
	url := fs.String("url", "https://127.0.0.1", "base URL of axond")
	token := fs.String("token", "", "API bearer token (falls back to $AX_TOKEN)")
	insecure := fs.Bool("insecure", false, "skip TLS verification")
	return func() (*client, error) {
		tok := *token
		if tok == "" {
			tok = os.Getenv("AX_TOKEN")
		}
		if tok == "" {
			return nil, fmt.Errorf("no API token: pass --token or set $AX_TOKEN")
		}
		tr := &http.Transport{
			//nolint:gosec // --insecure is an explicit opt-in for bootstrap certificates
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure},
		}
		return &client{
			baseURL: strings.TrimSuffix(*url, "/"),
			token:   tok,
			http:    &http.Client{Timeout: 30 * time.Second, Transport: tr},
		}, nil
	}
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ax: expected 'config get' or 'config put'")
		return 2
	}
	switch args[0] {
	case "get":
		return cmdConfigGet(args[1:], stdout, stderr)
	case "put":
		return cmdConfigPut(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ax: unknown config command %q (want get|put)\n", args[0])
		return 2
	}
}

func cmdConfigGet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ax config get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	newC := addFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := newC()
	if err != nil {
		fmt.Fprintf(stderr, "ax: %v\n", err)
		return 2
	}
	body, rev, err := c.get("/config")
	if err != nil {
		fmt.Fprintf(stderr, "ax: %v\n", err)
		return 1
	}
	_, _ = stdout.Write(body)
	fmt.Fprintf(stderr, "# revision %s\n", rev)
	return 0
}

func cmdConfigPut(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ax config put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	newC := addFlags(fs)
	file := fs.String("f", "", "configuration YAML file to apply")
	msg := fs.String("m", "", "commit message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(stderr, "ax: config put requires -f <file>")
		return 2
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(stderr, "ax: %v\n", err)
		return 1
	}
	c, err := newC()
	if err != nil {
		fmt.Fprintf(stderr, "ax: %v\n", err)
		return 2
	}
	rev, err := c.put("/config", data, *msg)
	if err != nil {
		fmt.Fprintf(stderr, "ax: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "committed %s\n", rev)
	return 0
}

// get performs an authenticated GET and returns the body and revision.
func (c *client) get(path string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", apiError(resp.StatusCode, body)
	}
	return body, resp.Header.Get("X-AxonWall-Revision"), nil
}

// put performs an authenticated PUT of the config document.
func (c *client) put(path string, data []byte, message string) (string, error) {
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/yaml")
	if message != "" {
		req.Header.Set("X-AxonWall-Message", message)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp.StatusCode, body)
	}
	var out struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("unexpected response: %w", err)
	}
	return out.Revision, nil
}

// apiError renders the API's JSON error envelope into a readable message.
func apiError(status int, body []byte) error {
	var env struct {
		Error  string   `json:"error"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		msg := env.Error
		if len(env.Issues) > 0 {
			msg += "\n  - " + strings.Join(env.Issues, "\n  - ")
		}
		return fmt.Errorf("API %d: %s", status, msg)
	}
	return fmt.Errorf("API %d: %s", status, strings.TrimSpace(string(body)))
}
