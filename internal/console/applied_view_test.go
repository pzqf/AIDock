package console

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
)

func TestAppliedViewAPIAuthenticatesAndKeepsExplicitBinding(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	if w := request(s, "GET", "/api/native/applied", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(s, "GET", "/api/native/applied?project=foreign&root=C:/", "", c, "", "")
	var out struct {
		OK    bool
		Items native.AppliedViews
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK || out.Items.Project != s.engine.SourceOwner || len(out.Items.Items) != 0 {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "POST", "/api/native/applied", "{}", c, "http://"+s.address, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
