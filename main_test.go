package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// newHandler against a fake Rill upstream + fake snapshot sidecar.
func newTestHandler(t *testing.T, rillURL, snapshotURL, token string) http.Handler {
	t.Helper()
	h, err := newHandler(config{
		rillUpstream:  rillURL,
		snapshotURL:   snapshotURL,
		snapshotToken: token,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h
}

func TestInjectsScriptIntoHTML(t *testing.T) {
	rill := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><h1>Rill</h1></body></html>")
	}))
	defer rill.Close()

	h := newTestHandler(t, rill.URL, "http://unused", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `<script src="/__ft/deploy-shim.js" defer></script></body>`) {
		t.Fatalf("script not injected before </body>: %q", body)
	}
	if got := rec.Header().Get("Content-Length"); got != "" && got != "0" {
		if want := len(body); got != strconv.Itoa(want) {
			t.Fatalf("Content-Length %s != body len %d", got, want)
		}
	}
}

func TestDoesNotInjectIntoNonHTML(t *testing.T) {
	rill := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, "console.log('app');")
	}))
	defer rill.Close()

	h := newTestHandler(t, rill.URL, "http://unused", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))

	if strings.Contains(rec.Body.String(), "deploy-shim.js") {
		t.Fatalf("must not inject into non-HTML: %q", rec.Body.String())
	}
}

func TestServesShimJS(t *testing.T) {
	h := newTestHandler(t, "http://unused", "http://unused", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__ft/deploy-shim.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/__ft/deploy") {
		t.Fatal("shim JS missing the deploy endpoint reference")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("unexpected content-type %q", ct)
	}
}

func TestRelaxCSP(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-x'; connect-src 'self'")
	relaxCSP(h)
	got := h.Get("Content-Security-Policy")
	if !strings.Contains(got, "script-src") || !strings.Contains(got, "'self'") {
		t.Fatalf("script-src not relaxed: %q", got)
	}
	// Existing connect-src 'self' must be preserved, not duplicated wrongly.
	if strings.Count(got, "connect-src") != 1 {
		t.Fatalf("connect-src mangled: %q", got)
	}
	// No CSP header => no-op, no panic.
	empty := http.Header{}
	relaxCSP(empty)
	if empty.Get("Content-Security-Policy") != "" {
		t.Fatal("relaxCSP invented a CSP header")
	}
}

func TestDeployTriggersSnapshot(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"created","key":"k","hash":"h"}`)
	}))
	defer sidecar.Close()

	h := newTestHandler(t, "http://unused", sidecar.URL, "secret-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__ft/deploy", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out["status"] != "created" {
		t.Fatalf("status = %q", out["status"])
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("bearer not forwarded: %q", gotAuth)
	}
	if gotPath != snapshotRPCPath {
		t.Fatalf("wrong RPC path: %q", gotPath)
	}
	if gotBody != "{}" {
		t.Fatalf("wrong body: %q", gotBody)
	}
}

func TestDeployUnchangedPassthrough(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"unchanged"}`)
	}))
	defer sidecar.Close()

	h := newTestHandler(t, "http://unused", sidecar.URL, "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__ft/deploy", nil))

	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != "unchanged" {
		t.Fatalf("status = %q", out["status"])
	}
}

func TestDeployConnectErrorMapped(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":"internal","message":"remote rejected push"}`)
	}))
	defer sidecar.Close()

	h := newTestHandler(t, "http://unused", sidecar.URL, "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__ft/deploy", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["error"] != "remote rejected push" {
		t.Fatalf("error not surfaced: %q", out["error"])
	}
}

func TestDeployMissingTokenIs503(t *testing.T) {
	h := newTestHandler(t, "http://unused", "http://unused", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__ft/deploy", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestDeployRejectsGET(t *testing.T) {
	h := newTestHandler(t, "http://unused", "http://unused", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__ft/deploy", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}
