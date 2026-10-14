// Package console serves a process-scoped, authenticated loopback UI.
package console

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/web"
)

type Server struct {
	engine               *engine.Engine
	address              string
	mu                   sync.Mutex
	workMu               sync.Mutex
	token, session, csrf string
	last                 time.Time
	started              time.Time
	connections          int
	closed               chan struct{}
	once                 sync.Once
}

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func New(e *engine.Engine, address string) *Server {
	return &Server{engine: e, address: address, token: random(), last: time.Now(), started: time.Now(), closed: make(chan struct{})}
}
func (s *Server) URL() string {
	path := "/"
	if s.engine.SourceRoot != "" || s.engine.GlobalRoot != "" {
		path = "/source.html"
	} else if s.engine.BridgeFile != "" {
		path = "/bridge.html"
	}
	return "http://" + s.address + path + "#token=" + s.token
}
func (s *Server) Close() { s.once.Do(func() { close(s.closed) }) }

// Cookies are host-scoped, not port-scoped. Independent loopback consoles
// must not replace each other's authentication when sharing one browser.
func (s *Server) cookieName() string {
	_, port, _ := net.SplitHostPort(s.address)
	return "aidock_session_" + port
}

func Serve(ctx context.Context, e *engine.Engine, ready func(string) error) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s := New(e, listener.Addr().String())
	h := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	if err = ready(s.URL()); err != nil {
		listener.Close()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(listener) }()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		case <-ctx.Done():
			s.Close()
		case <-s.closed:
			s.workMu.Lock()
			shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := h.Shutdown(shutdown)
			s.workMu.Unlock()
			return err
		case <-tick.C:
			s.mu.Lock()
			expired := time.Since(s.last) > 15*time.Minute || time.Since(s.started) > 6*time.Hour
			s.mu.Unlock()
			if expired {
				s.Close()
			}
		}
	}
}

func (s *Server) Handler() http.Handler {
	assets, _ := fs.Sub(web.Assets, "assets")
	static := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != s.address || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+s.address) {
			http.Error(w, "Host/Origin rejected", 403)
			return
		}
		if r.URL.Path == "/api/session" {
			if r.Method == http.MethodPost {
				s.exchange(w, r)
				return
			}
			if r.Method == http.MethodGet && s.auth(r, false) {
				s.mu.Lock()
				csrf := s.csrf
				s.mu.Unlock()
				writeJSON(w, 200, map[string]any{"csrf": csrf})
				return
			}
			http.Error(w, "Unauthorized", 401)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "Method not allowed", 405)
				return
			}
			static.ServeHTTP(w, r)
			return
		}
		if !s.auth(r, r.Method != http.MethodGet) {
			http.Error(w, "Unauthorized or invalid CSRF", 401)
			return
		}
		switch r.URL.Path {
		case "/api/state":
			if r.Method != http.MethodGet {
				http.Error(w, "Method not allowed", 405)
				return
			}
			writeJSON(w, 200, map[string]any{"version": engine.Version, "manifest": s.engine.Manifest, "roots": s.engine.Roots, "tools": engine.Tools(), "stage": "1A / 1B / 1C 子集", "write_enabled": s.engine.SourceRoot != "" || s.engine.GlobalRoot != "", "native_write_mode": "checked-skill-files", "bridge_file": s.engine.BridgeFile, "source_copy": s.engine.SourceCopy, "global_root": s.engine.GlobalRoot, "native_root": s.engine.NativeRoot})
		case "/api/native/plan", "/api/native/diff", "/api/native/apply":
			s.nativeAPI(w, r)
		case "/api/native/check", "/api/native/check/latest":
			s.nativeCheckAPI(w, r)
		case "/api/native/rehearsal/run", "/api/native/rehearsal/latest", "/api/native/rehearsal/plan", "/api/native/rehearsal/apply", "/api/native/rehearsal/recover":
			s.nativeRehearsalAPI(w, r)
		case "/api/native/transactions", "/api/native/recover":
			s.nativeTransactionsAPI(w, r)
		case "/api/native/rollback/plan", "/api/native/rollback/apply":
			s.nativeReverseAPI(w, r)
		case "/api/replica/list", "/api/replica/register", "/api/replica/compare", "/api/replica/baseline", "/api/replica/confirm", "/api/replica/recover", "/api/replica/resolve", "/api/replica/rehearsal", "/api/replica/rehearsal/latest", "/api/replica/rehearsal/plan", "/api/replica/rehearsal/apply", "/api/replica/rehearsal/recover":
			s.replicaAPI(w, r)
		case "/api/source/state", "/api/source/file", "/api/source/references", "/api/source/check", "/api/source/plan", "/api/source/apply", "/api/source/recover":
			s.sourceAPI(w, r)
		case "/api/bridge":
			if r.Method != http.MethodGet {
				http.Error(w, "Method not allowed", 405)
				return
			}
			v, err := s.engine.BridgeCheck(r.Context())
			if err != nil {
				out := result.New("skill bridge check", nil)
				out.OK, out.Code, out.Summary = false, "BRIDGE_CHECK_FAILED", err.Error()
				var classified *result.Error
				if errors.As(err, &classified) {
					out.Code = classified.Code
				}
				writeJSON(w, 200, out)
				return
			}
			// As with source check, HTTP success means the diagnostic ran. The
			// report's status still carries all static failures and drift.
			out := result.New("skill bridge check", v.Items)
			out.Summary = v.Summary
			writeJSON(w, 200, out)
		case "/api/inventory":
			if r.Method != http.MethodGet {
				http.Error(w, "Method not allowed", 405)
				return
			}
			q := r.URL.Query()
			o := inventory.Options{Project: q.Get("project"), Scope: q.Get("scope"), Copy: q.Get("copy"), IncludeGenerated: q.Get("include_generated") == "true"}
			v, err := s.engine.Inventory(r.Context(), o)
			if err != nil {
				writeJSON(w, 400, result.Result{SchemaVersion: 1, OK: false, Code: "SCAN_FAILED", Operation: "skill scan", Summary: err.Error(), Warnings: []string{}})
				return
			}
			writeJSON(w, 200, v)
		case "/api/events":
			if r.Method != http.MethodGet {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.events(w, r)
		case "/api/close":
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.workMu.Lock()
			defer s.workMu.Unlock()
			writeJSON(w, 200, map[string]bool{"closed": true})
			s.Close()
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "http://"+s.address || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "Invalid session request", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Token string `json:"token"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&body); err != nil {
		http.Error(w, "Invalid session request", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.started) > 5*time.Minute || s.token == "" || subtle.ConstantTimeCompare([]byte(s.token), []byte(body.Token)) != 1 {
		http.Error(w, "Expired or consumed token", 401)
		return
	}
	s.token = ""
	s.session = random()
	s.csrf = random()
	s.last = time.Now()
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]string{"csrf": s.csrf})
}

func (s *Server) auth(r *http.Request, write bool) bool {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) != 1 || time.Since(s.last) > 15*time.Minute || time.Since(s.started) > 6*time.Hour {
		return false
	}
	if write && (r.Header.Get("Origin") != "http://"+s.address || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-AIDock-CSRF")), []byte(s.csrf)) != 1) {
		return false
	}
	if r.URL.Path != "/api/events" {
		s.last = time.Now()
	}
	return true
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	s.mu.Lock()
	s.connections++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.connections--
		s.mu.Unlock()
		time.AfterFunc(8*time.Second, func() {
			s.mu.Lock()
			empty := s.connections == 0
			s.mu.Unlock()
			if empty {
				s.Close()
			}
		})
	}()
	_, _ = fmt.Fprint(w, "data: connected\n\n")
	f.Flush()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closed:
			return
		case <-t.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			f.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
