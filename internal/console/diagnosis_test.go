package console

import (
	"encoding/json"
	"testing"

	"github.com/pzqf/AIDock/internal/engine"
)

func TestDiagnosisAPIKeepsBindingAndReportsIncompleteWithoutWrite(t *testing.T) {
	s, c, csrf := sourceServer(t)
	if w := request(s, "GET", "/api/diagnosis", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/diagnosis", "{}", c, "http://"+s.address, csrf); w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request(s, "GET", "/api/diagnosis?root=C:/&project=other", "", c, "", "")
	var out struct {
		OK    bool
		Items engine.Diagnosis
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || !out.OK || out.Items.Root != s.engine.SourceRoot || out.Items.Project != s.engine.SourceOwner || out.Items.Status == "PASS" {
		t.Fatal(w.Body.String(), err)
	}
}
