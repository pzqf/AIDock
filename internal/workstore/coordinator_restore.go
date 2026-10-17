package workstore

import (
	"strings"

	"github.com/pzqf/AIDock/internal/result"
)

// RestoreSelected previews the BEFORE state of a committed recovery node.
// Unlike Reverse, the node may precede another partial restore. Reference is
// always the latest successful transaction, so applying an older preview still
// fails closed. A missing selection restores all roots; a present map restores
// only named roots/prefixes. Other roots retain their exact current images.
// The caller owns the policy/identity and scope selection; raw caller paths
// must never be passed here by an API or command without domain validation.
func (c *Coordinator) RestoreSelected(id string, selection map[string][]string) (BatchPlan, error) {
	var out BatchPlan
	if selection != nil && len(selection) == 0 {
		return out, result.Fail("INVALID_INPUT", "恢复需要非空的明确选择", 2)
	}
	for key, prefixes := range selection {
		if c.store(key) == nil {
			return out, result.Fail("INVALID_INPUT", "恢复选择不属于当前完整事务根", 2)
		}
		for _, prefix := range prefixes {
			if SafeRelative(prefix) != nil {
				return out, result.Fail("INVALID_INPUT", "恢复选择含不安全的相对路径", 2)
			}
		}
	}
	err := c.locked(func() error {
		if err := c.pending(); err != nil {
			return err
		}
		js, err := c.journals()
		if err != nil {
			return err
		}
		var original *BatchPlan
		latest := ""
		for _, j := range js {
			if j.State != "committed" {
				continue
			}
			if latest == "" {
				latest = j.Plan.ID
			}
			if j.Plan.ID == id {
				p := j.Plan
				original = &p
			}
		}
		if original == nil || original.Action == "seed" {
			return result.Fail("TRANSACTION_NOT_FOUND", "恢复节点缺少本批次的成功事务证明", 2)
		}
		if original.Action != "apply" && original.ID != latest {
			return result.Fail("REPLAY_UNDO_STALE", "只能撤销最新成功的回退/撤销事务；旧回退节点不能替代完整时间点恢复", 3)
		}
		observed := map[string]Tree{}
		for _, t := range original.Targets {
			now, err := c.capture(t)
			if err != nil {
				return err
			}
			observed[t.Path] = now
		}
		targets, err := selectedRestoreTargets(original.Targets, observed, selection)
		if err != nil {
			return err
		}
		action := "rollback"
		if original.Action == "rollback" {
			action = "undo-rollback"
		}
		out, err = c.preview(action, latest, targets)
		return err
	})
	return out, err
}

func selectedRestoreTargets(original []BatchTarget, observed map[string]Tree, selection map[string][]string) ([]BatchTarget, error) {
	targets := []BatchTarget{}
	changed := false
	for _, t := range original {
		now, ok := observed[t.Path]
		if !ok {
			return nil, result.Fail("INVALID_ROOTS", "恢复缺少完整的当前根观察", 2)
		}
		desired := Clone(now)
		prefixes, selected := selection[t.Path]
		selected = selection == nil || selected
		if selected {
			if t.Before.Exists && t.After.Exists && !now.Exists {
				return nil, result.Fail("RECOVERY_CONFLICT", "恢复所需原目录不可用: "+t.Path, 6)
			}
			for _, change := range Changes(t.Before, t.After) {
				included := len(prefixes) == 0
				for _, prefix := range prefixes {
					included = included || change.Path == prefix || strings.HasPrefix(change.Path, prefix+"/")
				}
				if !included {
					continue
				}
				v, exists := now.Entries[change.Path]
				matches := func(e *Entry) bool { return e == nil && !exists || e != nil && exists && v == *e }
				if matches(change.Before) {
					continue
				}
				if !matches(change.After) {
					return nil, result.Fail("RECOVERY_CONFLICT", "恢复范围内有未协调的修改: "+t.Path+"/"+change.Path, 6)
				}
				if change.Before == nil {
					delete(desired.Entries, change.Path)
				} else {
					desired.Entries[change.Path] = *change.Before
				}
			}
			if len(prefixes) == 0 {
				desired.Exists = t.Before.Exists || len(desired.Entries) > 0
			}
		}
		if err := validateTree(desired); err != nil {
			return nil, result.Fail("RECOVERY_CONFLICT", err.Error(), 6)
		}
		changed = changed || Digest(now) != Digest(desired)
		targets = append(targets, BatchTarget{Path: t.Path, File: t.File, Before: now, After: desired})
	}
	if !changed {
		return nil, result.Fail("SNAPSHOT_CURRENT", "所选恢复范围已经与该节点一致", 3)
	}
	return targets, nil
}

// CheckRestoration is an immutable proof gate for domain owners. The caller
// already owns the coordinator's complete locks; it performs no filesystem
// observation or mutation. Recomputing both local digests cannot widen a
// selection, replace an unrelated baseline, or invent a different after-image.
func (c *Coordinator) CheckRestoration(id, digest string, selection map[string][]string, plan BatchPlan) error {
	if err := c.validate(plan); err != nil {
		return err
	}
	js, err := c.journals()
	if err != nil {
		return err
	}
	var original *BatchPlan
	for _, j := range js {
		if j.Plan.ID == id && j.State == "committed" && j.Plan.Digest == digest {
			p := j.Plan
			original = &p
		}
	}
	if original == nil {
		return result.Fail("BACKUP_INVALID", "恢复节点的原成功事务证明缺失或变化", 5)
	}
	if original.Action != "apply" && plan.Reference != id {
		return result.Fail("REPLAY_UNDO_STALE", "撤销节点已不是最新成功事务", 3)
	}
	action := "rollback"
	if original.Action == "rollback" {
		action = "undo-rollback"
	}
	if plan.Action != action {
		return result.Fail("BACKUP_INVALID", "恢复动作与原节点不对应", 5)
	}
	observed := map[string]Tree{}
	for _, t := range plan.Targets {
		observed[t.Path] = t.Before
	}
	wanted, err := selectedRestoreTargets(original.Targets, observed, selection)
	if err != nil {
		return err
	}
	if len(wanted) != len(plan.Targets) {
		return result.Fail("BACKUP_INVALID", "恢复提案的根范围变化", 5)
	}
	for i, t := range wanted {
		actual := plan.Targets[i]
		if t.Path != actual.Path || t.File != actual.File || Digest(t.After) != Digest(actual.After) {
			return result.Fail("BACKUP_INVALID", "恢复提案不匹配原节点及明确选择", 5)
		}
	}
	return nil
}
