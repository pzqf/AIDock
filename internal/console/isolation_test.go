package console

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/pzqf/AIDock/internal/isolation"
)

func TestIsolationAPIStaticAuthenticationAndNoCallerCommands(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	for _, tc := range []struct {
		method, path, body, origin, csrf string
		want                             int
	}{{"POST", "/api/platform/check", `{"probe":true}`, origin, "", 401}, {"POST", "/api/platform/check", `{"probe":true}`, "https://evil.invalid", csrf, 403}, {"GET", "/api/platform/check", "", "", "", 405}, {"POST", "/api/platform/check/latest", `{}`, origin, csrf, 405}} {
		w := request(s, tc.method, tc.path, tc.body, c, tc.origin, tc.csrf)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := request(s, "POST", "/api/platform/check", `{"probe":true}`, nil, origin, csrf); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"probe":true,"exe":"bad.exe"}`, `{"probe":true,"root":"C:/"}`, `{"probe":true,"command":["bad"]}`, `{"probe":true,"network":true}`, `{"probe":true} {}`} {
		w := request(s, "POST", "/api/platform/check", body, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.OK || out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	if w := request(s, "POST", "/api/platform/check/recover", `{}`, c, origin, ""); w.Code != 401 {
		t.Fatal("recovery lacks CSRF", w.Code)
	}
	if w := request(s, "GET", "/api/platform/check/recover", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	wBad := request(s, "POST", "/api/platform/check/recover", `{"root":"C:/"}`, c, origin, csrf)
	var bad struct{ Code string }
	_ = json.Unmarshal(wBad.Body.Bytes(), &bad)
	if bad.Code != "INVALID_INPUT" {
		t.Fatal(wBad.Body.String())
	}
	wPending := request(s, "GET", "/api/platform/check/pending", "", c, "", "")
	var rows struct {
		OK    bool
		Items []isolation.Pending
	}
	_ = json.Unmarshal(wPending.Body.Bytes(), &rows)
	if !rows.OK || len(rows.Items) != 0 {
		t.Fatal(wPending.Body.String())
	}
	w := request(s, "POST", "/api/platform/check", `{"probe":false}`, c, origin, csrf)
	var out struct {
		OK    bool
		Code  string
		Items isolation.Report
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.OK || out.Code != "EVIDENCE_INCOMPLETE" || out.Items.Probe || out.Items.GeneratorReady || out.Items.Status == "PASS" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/platform/check/latest", "", c, "", "")
	var latest struct {
		OK    bool
		Items *isolation.Report
	}
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if !latest.OK || latest.Items != nil {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/api/platform/check/show", `{"check_id":"../escape"}`, c, origin, csrf)
	var invalid struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &invalid)
	if invalid.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{s.engine.SourceRoot, s.engine.Roots.Local} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("read/negative request wrote", path, err)
		}
	}
}
