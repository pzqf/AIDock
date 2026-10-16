package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func diagnosisFixture(t *testing.T) (*Engine, *source.Manager, string) {
	t.Helper()
	d := t.TempDir()
	e := &Engine{SourceRoot: filepath.Join(d, ".aidock"), SourceOwner: "11111111-1111-4111-8111-111111111111", SourceScope: "server", SourceCopy: "22222222-2222-4222-8222-222222222222", NativeRoot: d, Roots: config.Roots{Local: filepath.Join(t.TempDir(), "local")}}
	m, err := e.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "diagnosis fixture", Document: "PRIVATE_BODY_DO_NOT_EXPORT"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	id := p.SkillID
	p, err = m.Plan(context.Background(), source.Request{Action: "enable", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return e, m, id
}

func TestDiagnosisShowsLocalOriginsWithoutValuesOrNativeWrites(t *testing.T) {
	e, m, id := diagnosisFixture(t)
	p, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	local, err := m.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: p.Layers.LocalSHA256, ExpectedLayers: p.Layers.Digest, Profile: map[string]any{"schema_version": 2, "name": "local", "rules": []any{"PRIVATE_RULE_DO_NOT_EXPORT"}, "skills": map[string]any{"disable": []any{id}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProfile(context.Background(), "local", local.Plan.ID, local.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tools) != 2 || len(r.Local) == 0 || r.Status == "PASS" {
		t.Fatal(r)
	}
	for _, tool := range r.Tools {
		if tool.Discovery != "UNKNOWN" || tool.Session != "UNKNOWN" || tool.Capability != "UNKNOWN" {
			t.Fatal(tool)
		}
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"PRIVATE_BODY_DO_NOT_EXPORT", "PRIVATE_RULE_DO_NOT_EXPORT", "\"effective\"", "\"value\""} {
		if strings.Contains(string(b), secret) {
			t.Fatal("report leaked values", secret)
		}
	}
	after, err := m.Store.Capture(m.Store.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("source changed", err)
	}
	for _, dir := range []string{".agents", ".claude"} {
		if _, err = os.Stat(filepath.Join(e.NativeRoot, dir)); !os.IsNotExist(err) {
			t.Fatal("diagnosis created output", err)
		}
	}
}

func TestDiagnosisKeepsOtherSectionsWhenSourceCannotBeParsed(t *testing.T) {
	e, m, _ := diagnosisFixture(t)
	if err := os.WriteFile(filepath.Join(m.Store.Root, "profile.yaml"), []byte("schema_version: [PRIVATE_PARSER_VALUE_DO_NOT_EXPORT"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := e.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "ACTION_REQUIRED" || len(r.Sections) != 5 || r.Sections[0].Status != "FAIL" {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "PRIVATE_PARSER_VALUE_DO_NOT_EXPORT") {
		t.Fatal("parser value leaked")
	}
	data, err := os.ReadFile(filepath.Join(m.Store.Root, "profile.yaml"))
	if err != nil || !strings.Contains(string(data), "PRIVATE_PARSER_VALUE_DO_NOT_EXPORT") {
		t.Fatal("damaged source was modified", err)
	}
}

func TestDiagnosisNeverUsesStaleDiscoveryAsCurrentEvidence(t *testing.T) {
	e, m, id := diagnosisFixture(t)
	n, err := e.Native()
	if err != nil {
		t.Fatal(err)
	}
	old, err := n.Check(context.Background(), native.CheckRequest{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "extra.txt", Content: "changed after check"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	r, err := e.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range r.Tools {
		if row.Tool == "codex-cli" {
			if row.Evidence == nil || row.Evidence.ID != old.ID || !row.Evidence.Stale || row.Discovery != "UNKNOWN" || row.Session != "UNKNOWN" {
				t.Fatal(row)
			}
			return
		}
	}
	t.Fatal("missing Codex row")
}

func TestDiagnosisDirectsProtectedTargetToOriginalCoordinatedRecovery(t *testing.T) {
	e, m, _ := diagnosisFixture(t)
	marker := workstore.CoordinatedMarker(m.Store.Root)
	if err := os.WriteFile(marker, []byte("unavailable external transaction marker"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := e.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Recoveries) != 1 || r.Recoveries[0].Kind != "coordinated" || r.Status != "ACTION_REQUIRED" {
		t.Fatal(r)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "unavailable external transaction marker" {
		t.Fatal("marker changed", err)
	}
}

func TestDiagnosisUnreadableRegistryIsNotZeroCopies(t *testing.T) {
	e, _, _ := diagnosisFixture(t)
	r, err := e.Diagnose(context.Background())
	if err != nil || r.Copies == nil || *r.Copies != 0 {
		t.Fatal("readable empty registry should report zero", r, err)
	}
	m, err := e.Replicas()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(m.Control.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(m.Control.Root, "replicas.json"), []byte("PRIVATE_BROKEN_REGISTRY"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = e.Diagnose(context.Background())
	if err != nil || r.Copies != nil || r.Status != "ACTION_REQUIRED" {
		t.Fatal("unreadable registry must report unknown count", r, err)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "PRIVATE_BROKEN_REGISTRY") {
		t.Fatal("registry parser value leaked")
	}
}

func TestDiagnosisStaleReasonDoesNotExposeRawErrorValues(t *testing.T) {
	for _, reason := range []string{"无法重新核对当前输入；PRIVATE_PARSER_VALUE", "arbitrary private value"} {
		if got := diagnosisStaleReason(reason); strings.Contains(got, "PRIVATE") || strings.Contains(got, "private") || got == reason {
			t.Fatal("raw error escaped into diagnosis", got)
		}
	}
	if got := diagnosisStaleReason("工具安装或程序内容已变化"); got != "工具安装或程序内容已变化" {
		t.Fatal(got)
	}
}
