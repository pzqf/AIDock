package console

import (
	"encoding/json"
	"github.com/pzqf/AIDock/internal/replica"
	"testing"
)

func TestReplicaFullPublicationAPIAuthenticationMethodsAndInput(t *testing.T) {
	s, cookie, csrf := sourceServer(t)
	origin := "http://" + s.address
	paths := []string{"/api/replica/publication/full-plan", "/api/replica/publication/full-show", "/api/replica/publication/full-history"}
	for _, path := range paths {
		method := "POST"
		if path == paths[2] {
			method = "GET"
		}
		if w := request(s, method, path, `{}`, nil, origin, csrf); w.Code != 401 {
			t.Fatal("authentication missing", path, w.Code)
		}
		wrong := "GET"
		if method == wrong {
			wrong = "POST"
		}
		if w := request(s, wrong, path, `{}`, cookie, origin, csrf); w.Code != 405 {
			t.Fatal("wrong method", path, w.Code)
		}
	}
	for _, path := range paths[:2] {
		w := request(s, "POST", path, `{"digest":"x","digest":"PRIVATE_INPUT"}`, cookie, origin, csrf)
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v["code"] != "INVALID_INPUT" {
			t.Fatal(path, w.Code, w.Body.String(), err)
		}
	}
	w := request(s, "GET", paths[2], "", cookie, origin, csrf)
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v["ok"] != true || len(v["items"].([]any)) != 0 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	for _, action := range replica.FullPublicationOperationNames() {
		path := "/api/replica/publication/" + action
		if w = request(s, "POST", path, `{}`, nil, origin, csrf); w.Code != 401 {
			t.Fatal("missing auth", action, w.Code)
		}
		if w = request(s, "POST", path, `{}`, cookie, origin, "wrong-csrf"); w.Code != 401 {
			t.Fatal("missing csrf", action, w.Code)
		}
		if w = request(s, "GET", path, ``, cookie, origin, csrf); w.Code != 405 {
			t.Fatal("wrong method", action, w.Code)
		}
		w = request(s, "POST", path, `{"publication_id":"x","publication_id":"PRIVATE_INPUT"}`, cookie, origin, csrf)
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v["code"] != "INVALID_INPUT" {
			t.Fatal(action, w.Code, w.Body.String(), err)
		}
	}
}
