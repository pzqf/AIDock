package console

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func sourceServer(t *testing.T) (*Server, *http.Cookie, string) {
	t.Helper()
	d := t.TempDir()
	e := &engine.Engine{SourceRoot: filepath.Join(d, ".aidock"), SourceOwner: "11111111-1111-4111-8111-111111111111", SourceScope: "server", Roots: config.Roots{Local: filepath.Join(d, "local")}}
	s := New(e, "127.0.0.1:32123")
	t.Cleanup(s.Close)
	c, csrf := authenticate(t, s)
	return s, c, csrf
}
func TestSourceAPIUsesBoundEngineAndRejectsUnsafeRequests(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	req := `{"action":"create","name":"guide","description":"API example","document":"body"}`
	for _, x := range []struct {
		cookie       *http.Cookie
		origin, csrf string
		want         int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}, {c, "", csrf, 401}} {
		if w := request(s, "POST", "/api/source/plan", req, x.cookie, x.origin, x.csrf); w.Code != x.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"action":"create","root":"C:/"}`, `{"action":"create","project":"foreign"}`, req + req} {
		w := request(s, "POST", "/api/source/plan", body, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	if _, e := os.Stat(s.engine.SourceRoot); !os.IsNotExist(e) {
		t.Fatal("negative request created source")
	}
	w := request(s, "POST", "/api/source/plan", req, c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.Preview
	}
	if e := json.Unmarshal(w.Body.Bytes(), &preview); e != nil || !preview.OK {
		t.Fatal(w.Body.String(), e)
	}
	if preview.Items.Owner != s.engine.SourceOwner || preview.Items.Scope != "server" || preview.Items.Plan.Root != s.engine.SourceRoot {
		t.Fatal(preview)
	}
	m, e := s.engine.Sources()
	if e != nil {
		t.Fatal(e)
	}
	expected, e := m.Plan(context.Background(), source.Request{Action: "create", ID: preview.Items.SkillID, Name: "guide", Description: "API example", Document: "body"})
	if e != nil {
		t.Fatal(e)
	}
	if workstore.Digest(expected.Plan.After) != workstore.Digest(preview.Items.Plan.After) {
		t.Fatal("API differs from shared source engine")
	}
	for _, digest := range []string{"", "wrong"} {
		b, _ := json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": digest})
		w = request(s, "POST", "/api/source/apply", string(b), c, origin, csrf)
		var out struct{ OK bool }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.OK {
			t.Fatal("bad digest accepted")
		}
	}
	b, _ := json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/source/apply", string(b), c, origin, csrf)
	var out struct{ OK bool }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.OK {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/source/state?root=C:/", "", c, "", "")
	var state struct{ Items source.State }
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.Items.Root != s.engine.SourceRoot || len(state.Items.Index.Skills) != 1 {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/source/file?id="+preview.Items.SkillID+"&path=../project.yaml", "", c, "", "")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.OK {
		t.Fatal("traversal read accepted")
	}
	if w = request(s, "GET", "/source.html", "", nil, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestUnboundSourceAPINeverCreatesRoots(t *testing.T) {
	d := t.TempDir()
	s := New(&engine.Engine{Roots: config.Roots{Local: filepath.Join(d, "not-created")}}, "127.0.0.1:32123")
	defer s.Close()
	c, csrf := authenticate(t, s)
	w := request(s, "POST", "/api/source/recover", "{}", c, "http://"+s.address, csrf)
	var out struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Code != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(w.Body.String())
	}
	if _, e := os.Stat(s.engine.Roots.Local); !os.IsNotExist(e) {
		t.Fatal("unbound source created roots")
	}
}

func TestSourceLifecycleAPIAndReferenceAuthentication(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	planAndApply := func(req source.Request) source.Preview {
		t.Helper()
		b, _ := json.Marshal(req)
		w := request(s, "POST", "/api/source/plan", string(b), c, origin, csrf)
		var out struct {
			OK    bool
			Items source.Preview
		}
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil || !out.OK {
			t.Fatal(w.Body.String(), e)
		}
		b, _ = json.Marshal(map[string]string{"plan_id": out.Items.Plan.ID, "digest": out.Items.Plan.Digest})
		w = request(s, "POST", "/api/source/apply", string(b), c, origin, csrf)
		var saved struct{ OK bool }
		_ = json.Unmarshal(w.Body.Bytes(), &saved)
		if !saved.OK {
			t.Fatal(w.Body.String())
		}
		return out.Items
	}
	p := planAndApply(source.Request{Action: "create", Name: "guide", Description: "example"})
	url := "/api/source/references?id=" + p.SkillID
	if w := request(s, "GET", url, "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", url, "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	w := request(s, "GET", url, "", c, "", "")
	var refs struct {
		OK    bool
		Items source.Impact
	}
	if e := json.Unmarshal(w.Body.Bytes(), &refs); e != nil || !refs.OK || refs.Items.ReferenceScope != "CURRENT_BOUND_SOURCE" || len(refs.Items.Unknown) != 2 {
		t.Fatal(w.Body.String(), e)
	}
	name := "项目约定"
	tags := []string{"server"}
	planAndApply(source.Request{Action: "metadata", ID: p.SkillID, DisplayName: &name, Tags: &tags})
	rename := planAndApply(source.Request{Action: "rename", ID: p.SkillID, Name: "guide-v2"})
	if rename.Impact.NativeCleanup != "PENDING_NATIVE_APPLY" {
		t.Fatal(rename)
	}
	retirement := planAndApply(source.Request{Action: "retire", ID: p.SkillID, Reason: "unused"})
	if len(retirement.Plan.AssetEffects) != 1 {
		t.Fatal(retirement)
	}
	for _, body := range []string{`{"action":"delete","id":"` + p.SkillID + `"}`, `{"action":"enable","id":"` + p.SkillID + `"}`, `{"action":"reactivate","id":"` + p.SkillID + `","name":"ignored"}`, `{"action":"reactivate","id":"` + p.SkillID + `","name":""}`, `{"action":"reactivate","id":"` + p.SkillID + `","files":null}`} {
		w = request(s, "POST", "/api/source/plan", body, c, origin, csrf)
		var out struct{ OK bool }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.OK {
			t.Fatal("unsafe lifecycle request accepted", w.Body.String())
		}
	}
	planAndApply(source.Request{Action: "reactivate", ID: p.SkillID})
	m, e := s.engine.Sources()
	if e != nil {
		t.Fatal(e)
	}
	state, e := m.State()
	if e != nil || len(state.Retirements) != 0 || state.Index.Skills[0].DisplayName != name {
		t.Fatal(state, e)
	}
}
