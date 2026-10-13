package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/inventory"
)

func request(s *Server, method, path, body string, cookie *http.Cookie, origin, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+s.address+path, strings.NewReader(body))
	r.Host = s.address
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-AIDock-CSRF", csrf)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func authenticate(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := request(s, "POST", "/api/session", `{"token":"`+s.token+`"}`, nil, "http://"+s.address, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return cookies[0], body["csrf"]
}

func TestUnauthenticatedReadAndTokenReplayRejected(t *testing.T) {
	s := New(&engine.Engine{}, "127.0.0.1:32123")
	defer s.Close()
	token := s.token
	if w := request(s, "GET", "/api/state", "", nil, "", ""); w.Code != 401 {
		t.Fatal("unauthorized read allowed")
	}
	cookie, _ := authenticate(t, s)
	if w := request(s, "GET", "/api/state", "", cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/session", `{"token":"`+token+`"}`, nil, "http://"+s.address, ""); w.Code != 401 {
		t.Fatal("token reused")
	}
}

func TestOriginHostAndCSRF(t *testing.T) {
	s := New(&engine.Engine{}, "127.0.0.1:32123")
	defer s.Close()
	cookie, csrf := authenticate(t, s)
	for _, origin := range []string{"https://evil.invalid", "http://127.0.0.1:80"} {
		if w := request(s, "GET", "/api/state", "", cookie, origin, ""); w.Code != 403 {
			t.Fatal("cross origin accepted")
		}
	}
	r := httptest.NewRequest("GET", "http://evil.invalid/api/state", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding Host accepted")
	}
	if w := request(s, "POST", "/api/close", "", cookie, "http://"+s.address, ""); w.Code != 401 {
		t.Fatal("write without CSRF accepted")
	}
	if w := request(s, "POST", "/api/close", "", cookie, "http://"+s.address, csrf); w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("close failed")
	}
}

func TestStaticCSPAndNoInlineScripts(t *testing.T) {
	s := New(&engine.Engine{}, "127.0.0.1:32123")
	defer s.Close()
	w := request(s, "GET", "/", "", nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatal(csp)
	}
	if strings.Contains(w.Body.String(), "onclick=") {
		t.Fatal("inline event handlers")
	}
}

func TestConsoleAndCLIShareInventoryEngine(t *testing.T) {
	m, err := inventory.Load("../../examples/fixture.scan.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := New(&engine.Engine{Manifest: m}, "127.0.0.1:32123")
	defer s.Close()
	cookie, _ := authenticate(t, s)
	w := request(s, "GET", "/api/inventory?scope=client", "", cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Items inventory.Report `json:"items"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Items.Skills) != 1 || v.Items.Skills[0].Scope != "client" {
		t.Fatal(v.Items)
	}
	w = request(s, "GET", "/api/inventory?scope=undeclared&path=C:/", "", cookie, "", "")
	if w.Code != 400 {
		t.Fatal("client could expand source scope")
	}
}
