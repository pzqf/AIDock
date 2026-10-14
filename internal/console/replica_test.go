package console

import (
	"context"
	"encoding/json"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
	"testing"
)

func TestReplicaAPIStoresOnlyLocalRegistrationAndRequiresConfirmation(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "API"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(replica.Binding{Name: "current", Root: m.Store.Root, Excludes: []string{}})
	if w := request(s, "POST", "/api/replica/register", string(bytes), c, origin, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"name":"bad","root":"C:/","project":"foreign"}`, `{"from":"bad","targets":[],"root":"C:/"}`} {
		path := "/api/replica/register"
		if body[2:6] == "from" {
			path = "/api/replica/compare"
		}
		w := request(s, "POST", path, body, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "POST", "/api/replica/register", string(bytes), c, origin, csrf)
	var out struct {
		OK    bool
		Items workstore.Plan
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || out.Items.Kind != "replica-control" {
		t.Fatal(w.Body.String(), err)
	}
	control, err := s.engine.Replicas()
	if err != nil {
		t.Fatal(err)
	}
	r, err := control.List()
	if err != nil || len(r.Copies) != 0 {
		t.Fatal("preview registered copy", r, err)
	}
	requestBytes, _ := json.Marshal(map[string]string{"plan_id": out.Items.ID, "digest": out.Items.Digest})
	w = request(s, "POST", "/api/replica/confirm", string(requestBytes), c, origin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r, err = control.List()
	if err != nil || len(r.Copies) != 1 {
		t.Fatal(r, err)
	}
	after, err := m.Store.Capture(m.Store.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("registration changed source", err)
	}
	if w = request(s, "POST", "/api/replica/list", `{}`, c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
