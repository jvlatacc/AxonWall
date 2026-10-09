package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeAxond serves the two endpoints ax exercises, with token auth.
func fakeAxond(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"missing or invalid bearer token"}`)
			return
		}
		w.Header().Set("X-AxonWall-Revision", "abc123")
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, "version: 1\n")
	})
	mux.HandleFunc("PUT /config", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"missing or invalid bearer token"}`)
			return
		}
		if r.Header.Get("X-AxonWall-Message") != "test message" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"commit message must not be empty"}`)
			return
		}
		w.Header().Set("X-AxonWall-Revision", "def456")
		fmt.Fprint(w, `{"revision":"def456"}`)
	})
	return httptest.NewServer(mux)
}

func setArgs(t *testing.T, args []string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"ax"}, args...)
	t.Cleanup(func() { os.Args = old })
}

func TestRunConfigGet(t *testing.T) {
	srv := fakeAxond(t, "tok")
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"config", "get", "--url", srv.URL, "--token", "tok"})
	if code := run(os.Args[1:], &stdout, &stderr); code != 0 {
		t.Fatalf("config get exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version: 1") {
		t.Fatalf("stdout missing config body: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "abc123") {
		t.Fatalf("stderr missing revision: %q", stderr.String())
	}
}

func TestRunConfigGetAuthFailure(t *testing.T) {
	srv := fakeAxond(t, "tok")
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"config", "get", "--url", srv.URL, "--token", "wrong"})
	if code := run(os.Args[1:], &stdout, &stderr); code != 1 {
		t.Fatalf("config get with bad token exited %d, want 1: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "401") {
		t.Fatalf("stderr missing 401: %q", stderr.String())
	}
}

func TestRunConfigPut(t *testing.T) {
	srv := fakeAxond(t, "tok")
	defer srv.Close()

	file := t.TempDir() + "/cfg.yaml"
	if err := os.WriteFile(file, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"config", "put", "--url", srv.URL, "--token", "tok",
		"-f", file, "-m", "test message"})
	if code := run(os.Args[1:], &stdout, &stderr); code != 0 {
		t.Fatalf("config put exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "def456") {
		t.Fatalf("stdout missing new revision: %q", stdout.String())
	}
}

func TestRunConfigPutRequiresFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"config", "put", "--token", "tok"})
	if code := run(os.Args[1:], &stdout, &stderr); code != 2 {
		t.Fatalf("config put without -f exited %d, want 2", code)
	}
}

func TestRunRequiresToken(t *testing.T) {
	srv := fakeAxond(t, "tok")
	defer srv.Close()

	t.Setenv("AX_TOKEN", "")
	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"config", "get", "--url", srv.URL})
	if code := run(os.Args[1:], &stdout, &stderr); code != 2 {
		t.Fatalf("config get without token exited %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no API token") {
		t.Fatalf("stderr missing token guidance: %q", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	setArgs(t, []string{"bogus"})
	if code := run(os.Args[1:], &stdout, &stderr); code != 2 {
		t.Fatalf("unknown command exited %d, want 2", code)
	}
}
