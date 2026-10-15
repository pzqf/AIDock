package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func saveRuleFiles(t *testing.T, m *Manager, values []RuleFile) Preview {
	t.Helper()
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, RuleFiles: &values, ExpectedRulesSHA256: state.RulesSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRuleLibraryOrderedBytesUnknownFilesRollbackAndUndo(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "a"})
	orphan := filepath.Join(m.Store.Root, "rules", "handwritten.md")
	write(t, orphan, []byte("unregistered must stay"))
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil || len(state.UnmanagedRules) != 1 {
		t.Fatal(state, err)
	}
	values := []RuleFile{{ID: workstore.ID(), Name: "第一条", Enabled: true, Body: "# 原文\r\n保留正文与空行。\r\n\r\n"}, {ID: workstore.ID(), Name: "第二条", Enabled: true, Body: "第二条规则。"}, {ID: workstore.ID(), Name: "停用", Body: "不应编译"}}
	state.Profile["rules"] = []any{"兼容内联规则。"}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, RuleFiles: &values, ExpectedRulesSHA256: state.RulesSHA256})
	if err != nil {
		t.Fatal(err)
	}
	now, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("preview wrote source")
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err = m.Profile()
	if err != nil || !reflect.DeepEqual(state.RuleFiles, values) {
		t.Fatal(state, err)
	}
	bundle, err := m.Export()
	if err != nil || !reflect.DeepEqual(bundle.Rules, []string{"兼容内联规则。", values[0].Body, values[1].Body}) {
		t.Fatal(bundle.Rules, err)
	}
	if state.Report == nil || len(state.Report.Effective["rules"].([]any)) != 3 {
		t.Fatal(state.Report)
	}
	// Explicit order controls compilation; deleting one indexed rule leaves
	// unrelated Markdown files and the inline compatibility layer untouched.
	reordered := []RuleFile{values[1], values[0]}
	saveRuleFiles(t, m, reordered)
	bundle, err = m.Export()
	if err != nil || bundle.Rules[1] != values[1].Body {
		t.Fatal(bundle.Rules, err)
	}
	if _, err = os.Stat(filepath.Join(m.Store.Root, ruleFilePath(values[2].ID))); !os.IsNotExist(err) {
		t.Fatal("deleted rule remains", err)
	}
	changed, _ := m.Store.Capture(m.Store.Root)
	rolled := apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	now, _ = m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("rollback did not restore exact source tree")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: rolled.Plan.ID})
	now, _ = m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(changed) {
		t.Fatal("undo lost rule index, file bytes or order")
	}
	data, err := os.ReadFile(orphan)
	if err != nil || string(data) != "unregistered must stay" {
		t.Fatal(err)
	}
}

func TestRuleLibraryStaleDraftFailureRecoveryAndInvalidInputs(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "a"})
	values := []RuleFile{{ID: workstore.ID(), Name: "规则", Enabled: true, Body: "original"}}
	saveRuleFiles(t, m, values)
	state, _ := m.Profile()
	before, _ := m.Store.Capture(m.Store.Root)
	values[0].Body = "candidate"
	req := ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, RuleFiles: &values, ExpectedRulesSHA256: state.RulesSHA256}
	p, err := m.ProfilePlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	m.Store.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			return errors.New("injected rules save failure")
		}
		return nil
	}
	_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	if err == nil {
		t.Fatal("fault injection missed")
	}
	m.Store.Hook = nil
	if _, err = m.Recover(); err != nil {
		t.Fatal(err)
	}
	now, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("failed save left partial rule content")
	}
	p, err = m.ProfilePlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.Root, ruleFilePath(values[0].ID))
	write(t, path, []byte("external edit"))
	_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.ProfilePlan(context.Background(), req)
	code(t, err, "PLAN_STALE")
	state, _ = m.Profile()
	req.ExpectedRulesSHA256 = state.RulesSHA256
	for _, invalid := range []RuleFile{{ID: "../escape", Name: "x", Body: "x"}, {ID: values[0].ID, Name: "x", Body: "<!-- aidock:begin -->"}, {ID: values[0].ID, Name: "x", Body: "bad\x00text"}} {
		items := []RuleFile{invalid}
		req.RuleFiles = &items
		if _, err = m.ProfilePlan(context.Background(), req); err == nil {
			t.Fatal("invalid rule accepted", invalid)
		}
	}
	items := []RuleFile{values[0], values[0]}
	req.RuleFiles = &items
	_, err = m.ProfilePlan(context.Background(), req)
	code(t, err, "RULE_LIBRARY_INVALID")
	unknown := RuleFile{ID: workstore.ID(), Name: "occupied", Enabled: true, Body: "takeover"}
	write(t, filepath.Join(m.Store.Root, ruleFilePath(unknown.ID)), []byte("unowned"))
	state, _ = m.Profile()
	req.ExpectedRulesSHA256 = state.RulesSHA256
	items = []RuleFile{unknown}
	req.RuleFiles = &items
	_, err = m.ProfilePlan(context.Background(), req)
	code(t, err, "RULE_FILE_UNOWNED")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = m.Export()
	code(t, err, "RULE_FILE_MISSING")
}

func TestRuleLibraryNativeImportRequiresExplicitWholeDraft(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "a"})
	files := []RuleFile{{Name: "已有文件", Enabled: true, Body: "existing"}}
	saveRuleFiles(t, m, files)
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	body := "<!-- aidock:begin -->\nimported\n<!-- aidock:end -->"
	write(t, filepath.Join(filepath.Dir(m.Store.Root), "AGENTS.md"), []byte(body))
	req := ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, NativeRule: &NativeRuleEdit{Tool: "codex-cli", SHA256: workstore.Hash([]byte(body)), Body: "imported", Mode: "append"}}
	_, err = m.ProfilePlan(context.Background(), req)
	code(t, err, "RULE_LIBRARY_DECISION_REQUIRED")
	merged := []string{}
	req.NativeRule.MergedRules = &merged
	files = append(state.RuleFiles, RuleFile{Name: "显式导入", Enabled: true, Body: "edited after importing"})
	req.RuleFiles = &files
	req.ExpectedRulesSHA256 = state.RulesSHA256
	p, err := m.ProfilePlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	bundle, err := m.Export()
	if err != nil || !reflect.DeepEqual(bundle.Rules, []string{"existing", "edited after importing"}) {
		t.Fatal(bundle.Rules, err)
	}
}
