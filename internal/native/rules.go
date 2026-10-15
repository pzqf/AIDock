package native

import (
	"bytes"
	"errors"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/rules"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type OwnedRule struct {
	Path   string `json:"path"`
	Digest string `json:"managed_sha256"`
}
type RuleTarget struct {
	Path     string             `json:"path"`
	Before   workstore.Tree     `json:"before"`
	After    workstore.Tree     `json:"after"`
	Changes  []workstore.Change `json:"changes"`
	Prefix   string             `json:"handwritten_before"`
	Managed  string             `json:"managed"`
	Suffix   string             `json:"handwritten_after"`
	Override *RuleInput         `json:"override,omitempty"`
}
type RuleInput struct {
	Path   string         `json:"path"`
	Before workstore.Tree `json:"before"`
}

func rulePath(tool string) string {
	if tool == "claude-code" {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}
func (m *Manager) targetRule(t Target, bundle source.Export) (Target, error) {
	entry, ok := t.After.Entries[ownershipFile]
	if !ok && len(bundle.Rules) == 0 {
		return t, nil
	}
	own := Ownership{Version: 1, Owner: bundle.Owner, Scope: bundle.Scope, SourceRoot: bundle.Root, Tool: t.Adapter.ID, Packages: []OwnedPackage{}}
	if ok {
		data, err := m.Source.Store.Data(entry)
		if err != nil {
			return t, err
		}
		if err = strict(data, &own); err != nil {
			return t, err
		}
	} else {
		entry.Mode = 0644
	}
	if len(bundle.Rules) == 0 && own.Rule == nil {
		return t, nil
	}
	p := rulePath(t.Adapter.ID)
	root := filepath.Join(m.Root, p)
	for _, authority := range bundle.Authorities {
		if overlaps(root, authority) {
			return t, result.Fail("EXTERNAL_TARGET_OVERLAP", "规则文件与只读权威源重叠", 4)
		}
	}
	before, err := m.Source.Store.CaptureFile(root)
	if err != nil {
		return t, result.Fail("NATIVE_TARGET_UNSAFE", err.Error(), 5)
	}
	r := &RuleTarget{Path: p, Before: before, After: workstore.Clone(before), Changes: []workstore.Change{}}
	t.Rule = r
	block := func(err error) (Target, error) {
		e := &result.Error{Code: "RULES_COMPILE_FAILED", Message: err.Error()}
		var known *result.Error
		if errors.As(err, &known) {
			e = known
		}
		t.Findings = append(t.Findings, inventory.Finding{Status: "FAIL", Code: e.Code, Detail: e.Message})
		t.Status, t.After, t.Changes = "FAIL", workstore.Clone(t.Before), []workstore.Change{}
		return t, nil
	}
	if t.Adapter.ID == "codex-cli" {
		const override = "AGENTS.override.md"
		tree, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, override))
		if err != nil {
			return t, result.Fail("NATIVE_TARGET_UNSAFE", err.Error(), 5)
		}
		r.Override = &RuleInput{Path: override, Before: tree}
		if entry, ok := tree.Entries[workstore.FileContent]; ok {
			data, err := m.Source.Store.Data(entry)
			if err != nil {
				return t, err
			}
			if len(bytes.TrimSpace(data)) > 0 {
				return block(result.Fail("RULES_SHADOWED", "同目录 AGENTS.override.md 会遮盖 AGENTS.md；保留两个文件，先显式协调后再预览", 5))
			}
		}
	}
	owned := ""
	if own.Rule != nil {
		if own.Rule.Path != p || !validDigest.MatchString(own.Rule.Digest) {
			return block(result.Fail("RULES_OWNERSHIP_INVALID", "规则归属与工具目标不符", 5))
		}
		owned = own.Rule.Digest
	}
	var original []byte
	mode := uint32(0644)
	if e, ok := before.Entries[workstore.FileContent]; ok {
		original, err = m.Source.Store.Data(e)
		if err != nil {
			return t, err
		}
		mode = e.Mode
	}
	compiled, err := rules.Compile(original, bundle.Rules, owned)
	if err != nil {
		return block(err)
	}
	if err = m.Source.Store.PutFile(&r.After, workstore.FileContent, compiled.Output, mode); err != nil {
		return t, err
	}
	r.Prefix, r.Managed, r.Suffix = compiled.Prefix, compiled.Managed, compiled.Suffix
	r.Changes = workstore.Changes(r.Before, r.After)
	own.Rule = &OwnedRule{Path: p, Digest: compiled.Digest}
	if err = m.Source.Store.PutFile(&t.After, ownershipFile, jsonData(own), entry.Mode); err != nil {
		return t, err
	}
	t.Changes = workstore.Changes(t.Before, t.After)
	t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "RULES_SESSION_UNVERIFIED", Detail: "托管规则可独立预演、回退和撤销；安装还需当前精确版本的显式规则加载检查，技能发现不能代替规则证据"})
	if t.Status != "FAIL" {
		t.Status = "UNKNOWN"
	}
	return t, nil
}
