package native

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"

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
	Path           string             `json:"path"`
	Before         workstore.Tree     `json:"before"`
	After          workstore.Tree     `json:"after"`
	Changes        []workstore.Change `json:"changes"`
	Prefix         string             `json:"handwritten_before"`
	Managed        string             `json:"managed"`
	Suffix         string             `json:"handwritten_after"`
	Override       *RuleInput         `json:"override,omitempty"`
	ObservedDigest string             `json:"observed_digest,omitempty"`
	ObservedBody   *string            `json:"observed_body,omitempty"`
	Resolution     *RuleResolution    `json:"resolution,omitempty"`
}

type RuleResolution struct {
	Tool           string `json:"tool"`
	Action         string `json:"action"`
	ObservedDigest string `json:"observed_digest"`
}

func validateRuleResolutions(values []RuleResolution, adapters []Adapter) ([]RuleResolution, error) {
	seen := map[string]bool{}
	out := []RuleResolution{}
	for _, v := range values {
		known := false
		for _, a := range adapters {
			known = known || v.Tool == a.ID
		}
		if !known || seen[v.Tool] || v.Action != "overwrite" || !validDigest.MatchString(v.ObservedDigest) {
			return nil, result.Fail("INVALID_INPUT", "规则决策需要唯一已选工具、overwrite 动作和当前完整文件观察摘要", 2)
		}
		seen[v.Tool] = true
	}
	for _, a := range adapters {
		for _, v := range values {
			if v.Tool == a.ID {
				out = append(out, v)
			}
		}
	}
	return out, nil
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
func (m *Manager) targetRule(t Target, bundle source.Export, resolutions ...*RuleResolution) (Target, error) {
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
	r.ObservedDigest = workstore.Digest(before)
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
	if observed, e := rules.Inspect(original); e == nil {
		r.ObservedBody = &observed.Body
	}
	if len(resolutions) > 0 && resolutions[0] != nil {
		decision := resolutions[0]
		if decision.Tool != t.Adapter.ID || decision.ObservedDigest != r.ObservedDigest {
			return t, result.Fail("PLAN_STALE", "规则决策后文件已变化，请重新选择如何处理", 3)
		}
		if owned == "" && r.ObservedBody == nil {
			return t, result.Fail("RULES_RESOLUTION_UNNECESSARY", "没有托管归属或可接管区块，先生成普通预览", 2)
		}
		// Explicit replacement requires an unambiguous block, or a deleted owned
		// block. Its exact whole-file observation is part of the durable plan.
		observed, e := rules.Inspect(original)
		if e == nil {
			compiled, err = rules.Compile(original, bundle.Rules, observed.Digest)
		} else if strings.Index(string(original), "<!-- aidock:") < 0 {
			compiled, err = rules.Compile(original, bundle.Rules, "")
		}
		r.Resolution = decision
	}
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
