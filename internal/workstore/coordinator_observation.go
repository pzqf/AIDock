package workstore

import "github.com/pzqf/AIDock/internal/result"

// ObservePlan rechecks a pending proposal under the same complete lock set as
// Apply. It may populate observation blobs, but writes no target or journal.
// This is an observation only: Apply independently repeats every check.
func (c *Coordinator) ObservePlan(id, digest string) error {
	return c.locked(func() error {
		if err := c.pending(); err != nil {
			return err
		}
		p, err := c.load(id)
		if err != nil {
			return err
		}
		if digest == "" || p.Digest != digest {
			return result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要共同事务的完整摘要", 4)
		}
		js, err := c.journals()
		if err != nil {
			return err
		}
		current := ""
		for _, j := range js {
			if j.Plan.ID == id {
				return result.Fail("PLAN_CONSUMED", "共同提案已执行", 3)
			}
			if current == "" && j.State == "committed" {
				current = j.Plan.ID
			}
		}
		if p.Reference != "" && p.Reference != current {
			return result.Fail("PLAN_STALE", "共同回放的最新成功事务变化", 3)
		}
		if err = c.assets(p); err != nil {
			return err
		}
		for _, t := range p.Targets {
			now, err := c.capture(t)
			if err != nil {
				return err
			}
			if Digest(now) != Digest(t.Before) {
				return result.Fail("PLAN_STALE", "共同提案的原件已变化", 3)
			}
		}
		if c.Validate != nil {
			return c.Validate(p)
		}
		return nil
	})
}
