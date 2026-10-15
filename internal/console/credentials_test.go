package console

import (
	"net/http"
	"strings"
	"testing"
)

func TestCredentialAPIAuthStrictInputAndNoEcho(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	for _, x := range []struct {
		cookie        *http.Cookie
		origin, token string
		code          int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		w := request(s, "POST", "/api/credentials/change", `{"action":"add","name":"key","value":"PRIVATE_SENTINEL","confirm":true}`, x.cookie, x.origin, x.token)
		if w.Code != x.code || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") {
			t.Fatal(w.Code)
		}
	}
	for _, body := range []string{`{"value":"PRIVATE_SENTINEL","value":"other"}`, `{"Value":"PRIVATE_SENTINEL"}`, `{"value":null}`, `{"confirm":false,"confirm":true}`, `{"unexpected":"PRIVATE_SENTINEL"}`, `{"action":"delete","name":"key","confirm":true}`, `{}{}`} {
		w := request(s, "POST", "/api/credentials/change", body, c, origin, csrf)
		if !strings.Contains(w.Body.String(), `"ok":false`) || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") {
			t.Fatal(w.Body.String())
		}
	}
	for _, path := range []string{"/api/credentials/change", "/api/credentials/reconcile"} {
		if w := request(s, "GET", path, "", c, "", ""); w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	w := request(s, "GET", "/api/credentials/state", "", c, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Fatal(w.Body.String())
	}
}
