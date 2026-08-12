// Command rill-deploy-shim is a tiny reverse proxy that sits in front of Rill
// Developer, behind an authenticating proxy. It does two things and nothing
// else:
//
//  1. Injects <script src="/__ft/deploy-shim.js"> into Rill's HTML so the
//     built-in "Deploy" button (upstream's dead-end Rill Cloud CTA) is
//     intercepted client-side.
//  2. Serves POST /__ft/deploy, which triggers the configured snapshot/publish
//     service's TriggerSnapshot RPC. This is the actual "deploy".
//
// Everything else is proxied verbatim to Rill. The proxy is meant to run
// behind an authenticating proxy (its only client), so /__ft/deploy inherits
// that existing session gate and calls the downstream service directly with a
// bearer token.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed deployshim.js
var deployShimJS []byte

// TriggerSnapshot is a Connect unary RPC; over HTTP/1.1 with an
// application/json body it works against the downstream service's h2c server
// (Connect serves both HTTP/1.1 and HTTP/2 cleartext). The request message is
// empty.
const snapshotRPCPath = "/snapshot.v1.SnapshotService/TriggerSnapshot"

type config struct {
	listenAddr    string
	rillUpstream  string
	snapshotURL   string
	snapshotToken string
}

func loadConfig() config {
	return config{
		listenAddr:    env("LISTEN_ADDR", ":9009"),
		rillUpstream:  env("RILL_UPSTREAM", "http://rill:9009"),
		snapshotURL:   env("SNAPSHOT_URL", "http://rill-snapshot:8484"),
		snapshotToken: os.Getenv("SNAPSHOT_TOKEN"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// shutdownTimeout bounds the graceful drain. It must stay comfortably below
// the pod's terminationGracePeriodSeconds (30s) so we always finish on our own
// terms rather than being SIGKILLed mid-drain.
const shutdownTimeout = 10 * time.Second

func main() {
	// Catch SIGTERM ourselves. Without this the shim is PID 1 in a distroless
	// container with no handler installed, so the kernel ignores the default
	// disposition, Go's runtime.dieFromSignal falls through to its `exit(2)`
	// fallback, and every ordinary kubelet-initiated stop is recorded as
	// "Error (exit 2)" with no log line — indistinguishable from a crash.
	// That misreading cost a real investigation: 204 restarts were read as a
	// crashloop when they were routine restarts during node memory pressure.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, loadConfig()); err != nil {
		log.Fatal(err)
	}
}

// run serves until ctx is cancelled, then drains and returns nil. Any error
// returned is a genuine failure worth a non-zero exit.
func run(ctx context.Context, cfg config) error {
	handler, err := newHandler(cfg)
	if err != nil {
		return err
	}
	if cfg.snapshotToken == "" {
		// Not fatal: the proxy still serves Rill; only /__ft/deploy fails
		// (with a clear error) so the editor stays reachable if the token
		// is missing at boot.
		log.Print("warning: SNAPSHOT_TOKEN is empty — POST /__ft/deploy will return 503")
	}

	srv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("rill-deploy-shim listening on %s -> %s (snapshot %s)", cfg.listenAddr, cfg.rillUpstream, cfg.snapshotURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Print("signal received, draining")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		// Expected whenever a Rill SSE stream is open: a hijacked/streaming
		// connection never goes idle, so Shutdown waits out the full timeout.
		// Force it closed rather than hang until the SIGKILL.
		log.Printf("graceful shutdown incomplete (%v), closing", err)
		_ = srv.Close()
	}
	log.Print("shutdown complete")
	return nil
}

// newHandler builds the full request mux: the two /__ft/* routes plus a
// transparent reverse proxy to Rill for everything else.
func newHandler(cfg config) (http.Handler, error) {
	target, err := url.Parse(cfg.rillUpstream)
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Force an identity response so ModifyResponse can inject into
			// the HTML without having to decompress it.
			pr.Out.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: injectScript,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error for %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}

	deploy := &deployHandler{snapshotURL: cfg.snapshotURL, token: cfg.snapshotToken}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/__ft/deploy-shim.js", serveShimJS)
	mux.Handle("/__ft/deploy", deploy)
	mux.Handle("/", proxy)

	return mux, nil
}

func serveShimJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(deployShimJS)
}

// injectScript rewrites text/html responses to load the shim script and, if
// Rill sets a Content-Security-Policy, relaxes it enough for a same-origin
// script + fetch. All other responses (JS/CSS assets, the SSE stream, API
// JSON) pass through untouched.
func injectScript(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	// Defensive: never inject into a still-encoded body.
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()

	tag := []byte(`<script src="/__ft/deploy-shim.js" defer></script>`)
	if idx := bytes.LastIndex(body, []byte("</body>")); idx >= 0 {
		body = append(body[:idx], append(tag, body[idx:]...)...)
	} else {
		body = append(body, tag...)
	}

	relaxCSP(resp.Header)

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// relaxCSP ensures 'self' is allowed for scripts and fetches so the injected
// same-origin script and its POST to /__ft/deploy are not blocked. It only
// ever ADDS 'self' to an existing directive — it never removes anything and
// does nothing when there is no CSP header.
func relaxCSP(h http.Header) {
	csp := h.Get("Content-Security-Policy")
	if csp == "" {
		return
	}
	parts := strings.Split(csp, ";")
	for i, p := range parts {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if name != "script-src" && name != "connect-src" {
			continue
		}
		if !slices.Contains(fields[1:], "'self'") {
			parts[i] = " " + strings.Join(append(fields, "'self'"), " ")
		}
	}
	h.Set("Content-Security-Policy", strings.Join(parts, ";"))
}

// deployHandler proxies POST /__ft/deploy to the configured snapshot/publish
// service's TriggerSnapshot RPC and returns a compact JSON result to the
// browser.
type deployHandler struct {
	snapshotURL string
	token       string
	client      http.Client
}

func (d *deployHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if d.token == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "deploy not configured on the box"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.snapshotURL, "/")+snapshotRPCPath, strings.NewReader("{}"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("deploy: sidecar call failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the publish service"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode != http.StatusOK {
		// Connect errors are JSON: {"code":"...","message":"..."}.
		msg := "publish failed"
		var cerr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &cerr) == nil && cerr.Message != "" {
			msg = cerr.Message
		}
		log.Printf("deploy: sidecar returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}

	var out struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Status == "" {
		out.Status = "created"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": out.Status})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
