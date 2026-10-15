package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
)

func TestConfigurationAPIIsBoundAuthenticatedAndValueFree(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	origin := "http://" + s.address
	path := filepath.Join(s.engine.NativeRoot, ".mcp.json")
	original := []byte(`{"mcpServers":{"aidock-old":{"env":{"TOKEN":"VALUE_MUST_STAY_PRIVATE"}}}}`)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	body := `{"tools":["claude-code","codex-cli"]}`
	if w := request(s, "POST", "/api/native/config", body, c, origin, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/api/native/config", "", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	for _, bad := range []string{`{"tools":["codex-cli"],"root":"C:/"}`, `{"tools":["codex-cli"],"file":"../config.toml"}`, `{"tools":["unknown"]}`} {
		w := request(s, "POST", "/api/native/config", bad, c, origin, csrf)
		var response struct{ OK bool }
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		if response.OK {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "POST", "/api/native/config", body, c, origin, csrf)
	var response struct {
		OK    bool
		Items native.ConfigurationReport
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.OK || response.Items.Root != s.engine.NativeRoot || len(response.Items.Files) != 2 {
		t.Fatal(w.Body.String(), err)
	}
	if strings.Contains(w.Body.String(), "VALUE_MUST_STAY_PRIVATE") {
		t.Fatal("exposed private value")
	}
	read, _ := os.ReadFile(path)
	if string(read) != string(original) {
		t.Fatal("read changed configuration")
	}
	if err := os.WriteFile(path, []byte(`{"private":"VALUE_MUST_STAY_PRIVATE",`), 0644); err != nil {
		t.Fatal(err)
	}
	w = request(s, "POST", "/api/native/config", body, c, origin, csrf)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.OK || response.Items.Status != "FAIL" || response.Items.Files[1].State != "ABSENT" || strings.Contains(w.Body.String(), "VALUE_MUST_STAY_PRIVATE") {
		t.Fatal(w.Body.String(), err)
	}
}
