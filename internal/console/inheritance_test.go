package console

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/source"
)

func TestGlobalUIAndInheritedAPIShareFixedSourceBinding(t *testing.T) {
	d := t.TempDir()
	e := &engine.Engine{GlobalRoot: filepath.Join(d, "global"), Roots: config.Roots{Local: filepath.Join(d, "global-local")}}
	s := New(e, "127.0.0.1:32123")
	defer s.Close()
	if !strings.Contains(s.URL(), "/source.html") {
		t.Fatal("global UI used read-only scan page")
	}
	l, err := e.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.Plan(context.Background(), source.Request{Action: "create", Name: "http-guide", Description: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Apply(context.Background(), p.Plan.ID); err != nil {
		t.Fatal(err)
	}
	state, err := l.State()
	if err != nil {
		t.Fatal(err)
	}
	skill := state.Index.Skills[0]
	project, c, csrf := sourceServer(t)
	project.engine.LibraryRoot = e.GlobalRoot
	origin := "http://" + project.address
	for _, body := range []string{`{"action":"inherit","library_root":"other"}`, `{"action":"inherit","revision":null,"root":null}`} {
		w := request(project, "POST", "/api/source/plan", body, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	data, _ := json.Marshal(source.Request{Action: "inherit", ID: skill.ID, Revision: skill.Revision})
	w := request(project, "POST", "/api/source/plan", string(data), c, origin, csrf)
	var out struct {
		OK    bool
		Items source.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	data, _ = json.Marshal(map[string]string{"plan_id": out.Items.Plan.ID, "digest": out.Items.Plan.Digest})
	w = request(project, "POST", "/api/source/apply", string(data), c, origin, csrf)
	if !strings.Contains(w.Body.String(), `"committed"`) {
		t.Fatal(w.Body.String())
	}
	w = request(project, "GET", "/api/source/file?id="+skill.ID+"&path=SKILL.md", "", c, "", "")
	if !strings.Contains(w.Body.String(), `"read_only":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestConcurrentConsoleCookiesArePortScoped(t *testing.T) {
	a := New(&engine.Engine{}, "127.0.0.1:32124")
	b := New(&engine.Engine{}, "127.0.0.1:32125")
	defer a.Close()
	defer b.Close()
	ca, _ := authenticate(t, a)
	cb, _ := authenticate(t, b)
	if ca.Name == cb.Name {
		t.Fatal("cookies collide across ports")
	}
	if w := request(a, "GET", "/api/state", "", ca, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/api/state", "", cb, "", ""); w.Code != 401 {
		t.Fatal("foreign session accepted", w.Code)
	}
	if w := request(b, "GET", "/api/state", "", cb, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
