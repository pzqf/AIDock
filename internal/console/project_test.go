package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/project"
	"github.com/pzqf/AIDock/internal/source"
)

func bindingRequest(s *Server, cookie *http.Cookie, csrf, binding, method, path string, body any) *httptest.ResponseRecorder {
	bytes, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://"+s.address+path, strings.NewReader(string(bytes)))
	r.Host = s.address
	r.AddCookie(cookie)
	r.Header.Set("X-AIDock-Binding", binding)
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://"+s.address)
		r.Header.Set("X-AIDock-CSRF", csrf)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestProjectAPIAndConcurrentBindingsStayIsolated(t *testing.T) {
	d := t.TempDir()
	s := New(&engine.Engine{Roots: config.Roots{Local: filepath.Join(d, "local")}}, "127.0.0.1:32123")
	t.Cleanup(s.Close)
	cookie, csrf := authenticate(t, s)
	var bindings []project.Binding
	for _, name := range []string{"project-a", "project-b"} {
		root := filepath.Join(d, name)
		if e := os.Mkdir(root, 0700); e != nil {
			t.Fatal(e)
		}
		w := bindingRequest(s, cookie, csrf, "", "POST", "/api/projects/plan", project.Request{Root: root, Name: name, Create: true})
		var out struct {
			OK    bool
			Items project.Plan
		}
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil || !out.OK {
			t.Fatal(w.Body.String(), e)
		}
		if _, e := os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
			t.Fatal("preview changed project")
		}
		p := out.Items
		w = bindingRequest(s, cookie, csrf, "", "POST", "/api/projects/apply", map[string]string{"plan_id": p.ID, "digest": p.Digest})
		if !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(w.Body.String())
		}
		bindings = append(bindings, p.Binding)
	}
	call := func(binding, method, path string, body any) *httptest.ResponseRecorder {
		return bindingRequest(s, cookie, csrf, binding, method, path, body)
	}
	var previews [2]source.Preview
	var wg sync.WaitGroup
	for i, b := range bindings {
		wg.Add(1)
		go func(i int, b project.Binding) {
			defer wg.Done()
			w := call(b.ID, "POST", "/api/source/plan", source.Request{Action: "create", Name: "guide-" + b.Name, Description: "isolated request"})
			var v struct {
				OK    bool
				Items source.Preview
			}
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || !v.OK {
				t.Error(w.Body.String(), e)
				return
			}
			previews[i] = v.Items
		}(i, b)
	}
	wg.Wait()
	foreign := call(bindings[1].ID, "POST", "/api/source/apply", map[string]string{"plan_id": previews[0].Plan.ID, "digest": previews[0].Plan.Digest})
	if strings.Contains(foreign.Body.String(), `"ok":true`) {
		t.Fatal("foreign plan applied", foreign.Body.String())
	}
	for i, b := range bindings {
		p := previews[i].Plan
		w := call(b.ID, "POST", "/api/source/apply", map[string]string{"plan_id": p.ID, "digest": p.Digest})
		if !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(w.Body.String())
		}
	}
	for i, b := range bindings {
		w := call(b.ID, "GET", "/api/source/state", nil)
		var v struct{ Items source.State }
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		if v.Items.Root != b.Root || len(v.Items.Index.Skills) != 1 || v.Items.Index.Skills[0].ID != previews[i].SkillID {
			t.Fatal("cross-project state", w.Body.String())
		}
	}
	if s.engine.SourceRoot != "" {
		t.Fatal("request changed process-wide engine")
	}
	w := call(strings.Repeat("0", 64), "POST", "/api/source/plan", source.Request{Action: "create", Name: "wrong", Description: "must fail"})
	if !strings.Contains(w.Body.String(), "PROJECT_NOT_REGISTERED") {
		t.Fatal(w.Body.String())
	}
}
func TestProjectAPISecurityAndMethods(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	for _, x := range []struct {
		cookie       *http.Cookie
		origin, csrf string
		want         int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}, {c, "", csrf, 401}} {
		if w := request(s, "POST", "/api/projects/plan", `{}`, x.cookie, x.origin, x.csrf); w.Code != x.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/projects/list", "/api/projects/history"} {
		if w := request(s, "POST", path, `{}`, c, origin, csrf); w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{"/api/projects/plan", "/api/projects/apply", "/api/projects/recover"} {
		if w := request(s, "GET", path, "", c, "", ""); w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	for _, body := range []string{`{"root":"x","unknown":true}`, `{} {}`, `null`} {
		w := request(s, "POST", "/api/projects/plan", body, c, origin, csrf)
		if !strings.Contains(w.Body.String(), `"ok":false`) {
			t.Fatal(w.Body.String())
		}
	}
	// Recovery/list are machine-local, even if a tab's source binding is stale.
	w := bindingRequest(s, c, csrf, "invalid", "GET", "/api/projects/history", nil)
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestProjectEnginePreservesOnlyMatchingExplicitContext(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "a")
	os.Mkdir(root, 0700)
	m, _ := project.New(filepath.Join(d, "local"))
	p, e := m.Plan(context.Background(), project.Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p.ID, p.Digest); e != nil {
		t.Fatal(e)
	}
	b := p.Binding
	base := &engine.Engine{Roots: config.Roots{Local: m.Local}, SourceRoot: b.Root, SourceOwner: b.Project, SourceScope: b.Scope, LibraryRoot: "explicit-library", BridgeFile: "explicit-bridge"}
	same, e := base.ProjectEngine(b.ID)
	if e != nil || same.LibraryRoot != base.LibraryRoot || same.BridgeFile != base.BridgeFile {
		t.Fatal(same, e)
	}
	base.SourceRoot = filepath.Join(d, "other", ".aidock")
	other, e := base.ProjectEngine(b.ID)
	if e != nil || other.LibraryRoot != "" || other.BridgeFile != "" {
		t.Fatal(other, e)
	}
	// A damaged source must still resolve so domain history/repair can run.
	if e = os.WriteFile(filepath.Join(b.Root, "skills.yaml"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = base.ProjectEngine(b.ID); e != nil {
		t.Fatal("unavailable source hid registered recovery", e)
	}
}
