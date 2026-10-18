package replica

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type FullPublicationRestorationChecks struct {
	Copy   string                               `json:"copy_id"`
	Checks []native.PublicationRestorationCheck `json:"checks"`
}

type fullPublicationRegeneration struct {
	Seed       string                             `json:"seed"`
	SeedSHA    string                             `json:"seed_digest"`
	Executable string                             `json:"executable"`
	Home       string                             `json:"home"`
	Members    []FullPublicationRestorationChecks `json:"members"`
}

func (m *Manager) fullRestorationNative(p fullPublicationPlan, copy string) (*native.Manager, error) {
	for _, b := range fullPublicationBindings(p) {
		if b.ID != copy {
			continue
		}
		s, err := m.copySource(b)
		if err != nil {
			return nil, err
		}
		return native.New(s, filepath.Dir(b.Root))
	}
	return nil, result.Fail("REPLAY_COPY_SELECTION_INVALID", "恢复目标不属于原完整批次", 2)
}

// The bound seed is the ORIGINAL actual common restoration, not an isolated
// member plan. The returned opaque candidate permits only review/checking.
func (m *Manager) FullPublicationRestoreCandidate(ctx context.Context, publication, id, digest, copy, binding string) (native.PublicationRestorationCandidate, error) {
	p, err := m.loadFullPublication(publication, "")
	if err != nil {
		return native.PublicationRestorationCandidate{}, err
	}
	r, _, err := m.loadFullRestorationLevel(p, id, digest, false)
	if err != nil {
		return native.PublicationRestorationCandidate{}, err
	}
	if !containsCopy(r.Copies, copy) {
		return native.PublicationRestorationCandidate{}, result.Fail("REPLAY_COPY_SELECTION_INVALID", "原应用审查只能选择此恢复提案的明确成员", 2)
	}
	if err := m.fullRestorationIdentity(ctx, p, r.Copies); err != nil {
		return native.PublicationRestorationCandidate{}, err
	}
	n, err := m.fullRestorationNative(p, copy)
	if err != nil {
		return native.PublicationRestorationCandidate{}, err
	}
	return n.PublicationRestoreCandidate(ctx, p.Proof, r.Transaction.ID, r.Transaction.Digest, binding)
}

// Immutable structural guard. Exact managed values are subsequently rebuilt
// by native compilers, never trusted because a caller recomputed local hashes.
func (m *Manager) validateFullRegeneratedRole(seed, p fullPublicationRestoration) error {
	bad := func() error {
		return result.Fail("FULL_PUBLICATION_INVALID", "重新生成改变了原共同恢复的成员、前镜像、草稿或保留区域", 5)
	}
	g := p.Regenerated
	if g == nil || seed.Regenerated != nil || g.Seed != seed.Transaction.ID || g.SeedSHA != seed.Digest || p.Publication != seed.Publication || p.Original != seed.Original || p.OriginalSHA != seed.OriginalSHA || string(data(p.Copies)) != string(data(seed.Copies)) || p.Transaction.Action != seed.Transaction.Action || p.Transaction.Reference != seed.Transaction.Reference || len(p.Transaction.Targets) != len(seed.Transaction.Targets) || !filepath.IsAbs(g.Executable) || filepath.Clean(g.Executable) != g.Executable || filepath.Join(g.Home, "local") != m.Source.Store.Local || len(g.Members) == 0 {
		return bad()
	}
	regenerated := map[string]bool{}
	for _, member := range g.Members {
		if !containsCopy(seed.Copies, member.Copy) || regenerated[member.Copy] || len(member.Checks) == 0 {
			return bad()
		}
		regenerated[member.Copy] = true
		seen := map[string]bool{}
		for _, check := range member.Checks {
			if !fullPublicationSHA.MatchString(check.Binding) || !uuid.MatchString(check.ID) || !fullPublicationSHA.MatchString(check.Digest) || seen[check.Binding] {
				return bad()
			}
			seen[check.Binding] = true
		}
	}
	for i, target := range p.Transaction.Targets {
		old := seed.Transaction.Targets[i]
		if target.Path != old.Path || target.File != old.File || workstore.Digest(target.Before) != workstore.Digest(old.Before) {
			return bad()
		}
		allowed := false
		for copy := range regenerated {
			prefix := projectsnapshot.PublicationMemberPrefix(copy)
			allowed = allowed || target.Path == prefix+"native/.mcp.json" || target.Path == prefix+"native/.aidock-mcp-ownership.json"
			if target.Path == prefix+"native/.codex" {
				a, b := workstore.Clone(target.After), workstore.Clone(old.After)
				delete(a.Entries, "config.toml")
				delete(b.Entries, "config.toml")
				// Container existence can change only through its one generated
				// file; no other sibling or directory-mode change is permitted.
				a.Exists, b.Exists = true, true
				if workstore.Digest(a) != workstore.Digest(b) {
					return bad()
				}
				allowed = true
			}
		}
		if !allowed && workstore.Digest(target.After) != workstore.Digest(old.After) {
			return bad()
		}
	}
	return nil
}

func (m *Manager) reviewFullRestoration(ctx context.Context, p fullPublicationPlan, r fullPublicationRestoration) ([]native.PublicationRestorationMaterial, []native.PublicationRegeneratedRestoration, []FullPublicationRestorationMember, error) {
	if r.Regenerated == nil {
		materials, members, err := m.fullRestorationMaterials(ctx, p, r.Transaction, r.Copies)
		return materials, nil, members, err
	}
	g := r.Regenerated
	seed, _, err := m.loadFullRestorationLevel(p, g.Seed, g.SeedSHA, false)
	if err != nil {
		return nil, nil, nil, err
	}
	checks := map[string][]native.PublicationRestorationCheck{}
	for _, member := range g.Members {
		checks[member.Copy] = member.Checks
	}
	materials := []native.PublicationRestorationMaterial{}
	regenerated := []native.PublicationRegeneratedRestoration{}
	members := []FullPublicationRestorationMember{}
	var firstError error
	byPath := map[string]workstore.BatchTarget{}
	for _, target := range r.Transaction.Targets {
		byPath[target.Path] = target
	}
	for _, b := range fullPublicationBindings(p) {
		if !containsCopy(r.Copies, b.ID) {
			continue
		}
		member := FullPublicationRestorationMember{Copy: b, Views: []native.PublicationRestorationView{}}
		err := func() error {
			n, err := m.fullRestorationNative(p, b.ID)
			if err != nil {
				return err
			}
			original, err := n.InspectPublicationRestoration(ctx, p.Proof, seed.Transaction.ID, seed.Transaction.Digest)
			if err != nil {
				return err
			}
			member.Views = original.Views()
			if selected, ok := checks[b.ID]; ok {
				material, err := n.CompilePublicationRestoration(ctx, p.Proof, seed.Transaction.ID, seed.Transaction.Digest, selected, g.Executable, g.Home)
				if err != nil {
					return err
				}
				for _, target := range material.Targets() {
					target.Path = projectsnapshot.PublicationMemberPrefix(b.ID) + target.Path
					if string(data(target)) != string(data(byPath[target.Path])) {
						return result.Fail("PLAN_STALE", "重新生成的完整成员材料与真实共同提案不一致", 3)
					}
				}
				regenerated = append(regenerated, material)
				return nil
			}
			if err = original.Recheck(ctx); err != nil {
				return err
			}
			materials = append(materials, original)
			return nil
		}()
		if err != nil {
			member.Code = "FULL_PUBLICATION_RESTORE_REVIEW_REQUIRED"
			var failure *result.Error
			if errors.As(err, &failure) {
				member.Code = failure.Code
			}
			if firstError == nil {
				firstError = result.Fail(member.Code, "完整批次恢复目标 "+b.ID+" 需要重新核对："+member.Code, 4)
			}
		}
		members = append(members, member)
	}
	return materials, regenerated, members, firstError
}

func (m *Manager) PlanRegeneratedFullPublicationRestoration(ctx context.Context, publication, id, digest string, checks []FullPublicationRestorationChecks, executable, home string) (FullPublicationRestorationPreview, error) {
	p, err := m.loadFullPublication(publication, "")
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	seed, _, err := m.loadFullRestorationLevel(p, id, digest, false)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	if err = retainFullRestorationImages(c, seed.Transaction); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	if err := m.fullRestorationIdentity(ctx, p, seed.Copies); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	// Normalize only order. Duplicates and missing/foreign reviews remain errors.
	ordered := append([]FullPublicationRestorationChecks{}, checks...)
	for i := range ordered {
		ordered[i].Checks = append([]native.PublicationRestorationCheck{}, ordered[i].Checks...)
		sort.Slice(ordered[i].Checks, func(a, b int) bool { return ordered[i].Checks[a].Binding < ordered[i].Checks[b].Binding })
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Copy < ordered[j].Copy })
	r := seed
	r.Transaction.Targets = make([]workstore.BatchTarget, len(seed.Transaction.Targets))
	for i, t := range seed.Transaction.Targets {
		t.Before, t.After = workstore.Clone(t.Before), workstore.Clone(t.After)
		r.Transaction.Targets[i] = t
	}
	r.Regenerated = &fullPublicationRegeneration{Seed: id, SeedSHA: digest, Executable: executable, Home: home, Members: ordered}
	if err := m.validateFullRegeneratedRole(seed, r); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	for _, member := range ordered {
		n, err := m.fullRestorationNative(p, member.Copy)
		if err != nil {
			return FullPublicationRestorationPreview{}, err
		}
		compiled, err := n.CompilePublicationRestoration(ctx, p.Proof, seed.Transaction.ID, seed.Transaction.Digest, member.Checks, executable, home)
		if err != nil {
			return FullPublicationRestorationPreview{}, err
		}
		byPath := map[string]workstore.BatchTarget{}
		for _, target := range compiled.Targets() {
			target.Path = projectsnapshot.PublicationMemberPrefix(member.Copy) + target.Path
			byPath[target.Path] = target
		}
		for i, target := range r.Transaction.Targets {
			if next, ok := byPath[target.Path]; ok {
				r.Transaction.Targets[i] = next
			}
		}
	}
	if err := m.validateFullRegeneratedRole(seed, r); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	tx, err := c.ReviseReversal(seed.Transaction.ID, seed.Transaction.Digest, r.Transaction.Targets)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	r.Transaction = tx
	r.Digest = fullRestorationDigest(r)
	if err = saveLocal(m.fullRestorationFile(p.ID, tx.ID), r); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	return m.ReadFullPublicationRestoration(ctx, p.ID, tx.ID, r.Digest)
}
