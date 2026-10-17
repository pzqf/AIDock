package workstore

import "github.com/pzqf/AIDock/internal/result"

// ReviseReversal preserves an unconsumed reversal's exact before-images,
// scope and committed reference while a checked compiler refreshes desired
// managed output. The caller must copy every immutable asset into Blobs.
func (c *Coordinator) ReviseReversal(id, digest string, targets []BatchTarget) (BatchPlan, error) {
	var out BatchPlan
	err := c.locked(func() error {
		if err := c.pending(); err != nil {
			return err
		}
		original, err := c.load(id)
		if err != nil {
			return err
		}
		if original.Digest != digest || (original.Action != "rollback" && original.Action != "undo-rollback") || original.Reference == "" {
			return result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要原回退提案的明确摘要与引用", 4)
		}
		js, err := c.journals()
		if err != nil {
			return err
		}
		current := ""
		for _, j := range js {
			if j.Plan.ID == id {
				return result.Fail("PLAN_CONSUMED", "原回退提案已执行", 3)
			}
			if current == "" && j.State == "committed" {
				current = j.Plan.ID
			}
		}
		if current != original.Reference {
			return result.Fail("PLAN_STALE", "回退提案的最新成功引用变化", 3)
		}
		if len(targets) != len(original.Targets) {
			return result.Fail("INVALID_ROOTS", "重新生成必须保留全部共同根", 2)
		}
		before := map[string]BatchTarget{}
		for _, t := range original.Targets {
			before[t.Path] = t
		}
		seen := map[string]bool{}
		for _, t := range targets {
			old, ok := before[t.Path]
			if !ok || seen[t.Path] || t.File != old.File || Digest(t.Before) != Digest(old.Before) {
				return result.Fail("PLAN_STALE", "重新生成改变了原回退的作用域或前镜像", 3)
			}
			seen[t.Path] = true
		}
		out, err = c.draftReference(original.Action, original.Reference, targets)
		return err
	})
	return out, err
}
