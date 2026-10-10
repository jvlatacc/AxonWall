package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doHeader issues an HTTP request with one extra header, for the
// optimistic-concurrency guard tests.
func doHeader(t *testing.T, h http.Handler, method, path, bearer, name, value string, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if name != "" {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// The UI's optimistic-concurrency contract: a write that carries the
// revision it was based on must be rejected with 409 when the store has
// moved on, and accepted when the revision is current. Writes without the
// header keep working (the ax CLI does not send it).
func TestPutConfigHonorsExpectedRevision(t *testing.T) {
	s, srv := newTestServer(t, nil)
	h := srv.Handler()

	rev := func() string {
		r, err := s.Rev()
		if err != nil {
			t.Fatalf("Rev: %v", err)
		}
		return r
	}

	current := rev()

	// A stale revision is rejected and commits nothing.
	stale := doHeader(t, h, http.MethodPut, "/config", token, "X-AxonWall-Revision", "rev-does-not-exist", []byte(updatedYAML))
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("PUT with stale revision = %d, want 409", stale.StatusCode)
	}
	var conflict struct {
		Error   string `json:"error"`
		Current string `json:"current"`
	}
	if err := json.NewDecoder(stale.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if conflict.Current != current {
		t.Fatalf("409 body current = %q, want %q", conflict.Current, current)
	}
	if after := rev(); after != current {
		t.Fatalf("rejected PUT changed the store: %q -> %q", current, after)
	}

	// The current revision is accepted.
	okResp := doHeader(t, h, http.MethodPut, "/config", token, "X-AxonWall-Revision", current, []byte(updatedYAML))
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT with current revision = %d, want 200", okResp.StatusCode)
	}
	if okResp.Header.Get("X-AxonWall-Revision") == "" {
		t.Fatal("accepted PUT did not return a revision header")
	}

	// A headerless PUT still applies (CLI compatibility).
	noHeader := doHeader(t, h, http.MethodPut, "/config", token, "", "", []byte(baseYAML))
	if noHeader.StatusCode != http.StatusOK {
		t.Fatalf("PUT without revision header = %d, want 200", noHeader.StatusCode)
	}
}
