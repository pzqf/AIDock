package console

import (
	"encoding/json"
	"testing"

	"github.com/pzqf/AIDock/internal/engine"
)

func TestAuditAPIAuthenticationMethodsBindingAndInvalidFilters(t *testing.T) {
	s, c, csrf := sourceServer(t)
	if w := request(s, "GET", "/api/audit", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/audit", "{}", c, "http://"+s.address, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	for _, query := range []string{"page=bad", "page=-1", "category=unknown", "page=1&page=2"} {
		w := request(s, "GET", "/api/audit?"+query, "", c, "", "")
		var v struct {
			OK   bool
			Code string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v.OK || v.Code != "INVALID_INPUT" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(s, "GET", "/api/audit?root=C:/&project=foreign", "", c, "", "")
	var out struct {
		OK    bool
		Items engine.AuditReport
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || !out.OK || out.Items.Root != s.engine.SourceRoot || out.Items.Project != s.engine.SourceOwner || len(out.Items.Chains) != 5 {
		t.Fatal(w.Body.String(), err)
	}
}
