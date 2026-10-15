package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestProfileAPIConfirmationAndSecurity(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/api/profile/state", "", c, "", "")
	var out struct {
		OK    bool
		Items source.ProfileState
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	out.Items.Profile["name"] = "API Profile"
	body, _ := json.Marshal(source.ProfileEdit{ExpectedSHA256: out.Items.SHA256, Profile: out.Items.Profile})
	for _, x := range []struct {
		c            *http.Cookie
		origin, csrf string
		status       int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		if w = request(s, "POST", "/api/profile/plan", string(body), x.c, x.origin, x.csrf); w.Code != x.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w = request(s, "POST", "/api/profile/state", "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w = request(s, "GET", "/api/profile/plan", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = request(s, "POST", "/api/profile/plan", string(body), c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.OK {
		t.Fatal(w.Body.String(), err)
	}
	current, _ := m.Profile()
	if current.Profile["name"] == "API Profile" {
		t.Fatal("preview saved profile")
	}
	confirm, _ := json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/source/apply", string(confirm), c, origin, csrf)
	var applied struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &applied); err != nil || !applied.OK {
		t.Fatal(w.Body.String(), err)
	}
	current, err = m.Profile()
	if err != nil || current.Profile["name"] != "API Profile" {
		t.Fatal(current, err)
	}
}
