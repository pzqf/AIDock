package console

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/engine"
)

func TestReadOnlyBridgeConsoleSharesEngineAndRejectsScopeExpansion(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "external")
	if err := os.MkdirAll(filepath.Join(root, "skills/guide"), 0700); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "skills/guide/SKILL.md")
	original := []byte("---\nname: guide\ndescription: fixture\n---\n")
	if err := os.WriteFile(doc, original, 0600); err != nil {
		t.Fatal(err)
	}
	reg := bridge.Registration{ID: "33333333-3333-4333-8333-333333333333", Project: "11111111-1111-4111-8111-111111111111", Scope: "server", Copy: "test", Root: root, SourcePath: "skills", SourceRole: "external-source", Format: "native-skill"}
	data, _ := json.Marshal(bridge.Index{Version: 1, Bridges: []bridge.Registration{reg}})
	file := filepath.Join(d, "bridge.yaml")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{BridgeFile: file}
	s := New(e, "127.0.0.1:32123")
	defer s.Close()
	if !strings.Contains(s.URL(), "/bridge.html#token=") {
		t.Fatal(s.URL())
	}
	if w := request(s, "GET", "/api/bridge", "", nil, "", ""); w.Code != 401 {
		t.Fatal("unauthenticated external scan allowed")
	}
	cookie, csrf := authenticate(t, s)
	if w := request(s, "POST", "/api/bridge", "{}", cookie, "http://"+s.address, csrf); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	w := request(s, "GET", "/api/bridge?root=C:/&file=C:/&scope=foreign", "", cookie, "", "")
	var out struct {
		OK    bool          `json:"ok"`
		Items bridge.Report `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || len(out.Items.Bridges) != 1 {
		t.Fatal(w.Body.String(), err)
	}
	expected, err := e.BridgeCheck(context.Background())
	if err != nil || expected.Items.(bridge.Report).Bridges[0].CurrentRevision != out.Items.Bridges[0].CurrentRevision || out.Items.Bridges[0].Root != root {
		t.Fatal("HTTP differs from bound engine", err)
	}
	got, err := os.ReadFile(doc)
	if err != nil || string(got) != string(original) {
		t.Fatal("bridge API changed external bytes", err)
	}
	if w := request(s, "GET", "/bridge.html", "", nil, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "onclick=") {
		t.Fatal(w.Body.String())
	}
}

func TestUnboundBridgeConsoleDoesNotClaimSuccessfulScan(t *testing.T) {
	s := New(&engine.Engine{}, "127.0.0.1:32123")
	defer s.Close()
	c, _ := authenticate(t, s)
	w := request(s, "GET", "/api/bridge", "", c, "", "")
	if !strings.Contains(w.Body.String(), `"code":"BRIDGE_BINDING_REQUIRED"`) || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatal(w.Body.String())
	}
}
