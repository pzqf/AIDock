package native

import (
	"context"
	"strings"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// PublicationRestorationMaterial is one member's authority review, not a
// transaction owner or write permit. The replica owner must independently
// validate the original recovery node, selection and common baseline through
// Coordinator.CheckRestoration before applying its real common plan.
type PublicationRestorationMaterial struct {
	manager *Manager
	proof   projectsnapshot.PublicationCommit
	plan    workstore.BatchPlan
	member  workstore.BatchPlan
	views   []appliedRecord
}

type PublicationRestorationView struct {
	Binding  string `json:"applied_binding"`
	Snapshot string `json:"snapshot"`
	Commit   string `json:"commit"`
}

func (p PublicationRestorationMaterial) Views() []PublicationRestorationView {
	out := []PublicationRestorationView{}
	for _, a := range p.views {
		out = append(out, PublicationRestorationView{Binding: a.Record.ActiveKey, Snapshot: a.Record.ID, Commit: a.Record.Commit})
	}
	return out
}

// The only input images come from an actual saved plan of the bound common
// coordinator. Callers cannot supply native JSON/TOML or reintroduce grants.
// Old launch parameters are usable only with their exact current authority;
// otherwise the owner must arrange an explicit review and regeneration first.
func (m *Manager) InspectPublicationRestoration(ctx context.Context, proof projectsnapshot.PublicationCommit, id, digest string) (PublicationRestorationMaterial, error) {
	var out PublicationRestorationMaterial
	bad := func() error {
		return result.Fail("PUBLICATION_RESTORE_INVALID", "恢复需要原共同批次、明确成员及该批次的真实回退/撤销提案", 5)
	}
	if !validID.MatchString(id) || !validDigest.MatchString(digest) || proof.Transaction.Action != "apply" {
		return out, bad()
	}
	state, err := proof.State(m.Source.Store.Local)
	if err != nil {
		return out, err
	}
	if state != "committed" {
		return out, result.Fail("APPLIED_VIEW_NOT_COMMITTED", "缺少原真实副本全批提交，不读取恢复视图", 4)
	}
	binding := projectsnapshot.Binding{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, NativeRoot: m.Root}
	found := false
	for _, member := range proof.Members {
		found = found || member == binding
	}
	if !found {
		return out, bad()
	}
	c, err := proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		return out, err
	}
	p, err := c.LoadPlan(id)
	if err != nil {
		return out, err
	}
	if p.Digest != digest || p.Action != "rollback" && p.Action != "undo-rollback" {
		return out, bad()
	}
	if err = c.ObservePlan(id, digest); err != nil {
		return out, err
	}
	// A local image projection is used only by the existing ledger reader. It
	// is never saved as a member plan, journal, committed record or archive.
	memberImages := workstore.BatchPlan{ID: p.ID, Root: m.Root, Targets: []workstore.BatchTarget{}}
	prefix := projectsnapshot.PublicationMemberPrefix(m.Source.Copy)
	for _, target := range p.Targets {
		if !strings.HasPrefix(target.Path, prefix) {
			continue
		}
		target.Path = strings.TrimPrefix(target.Path, prefix)
		memberImages.Targets = append(memberImages.Targets, target)
	}
	if len(memberImages.Targets) != 9 {
		return out, bad()
	}
	before, err := m.publicationRestorationLedger(c.Blobs, memberImages, false)
	if err != nil {
		return out, err
	}
	after, err := m.publicationRestorationLedger(c.Blobs, memberImages, true)
	if err != nil {
		return out, err
	}
	views, err := m.restorationViews(ctx, before, after)
	if err != nil {
		return out, err
	}
	// Clone caller-owned maps/slices. A caller changing its copy later cannot
	// change what this opaque member review will validate under common locks.
	if err = strict(jsonData(proof), &out.proof); err != nil {
		return PublicationRestorationMaterial{}, err
	}
	out.manager, out.plan, out.member, out.views = m, p, memberImages, make([]appliedRecord, len(views))
	for i, view := range views {
		a, err := m.loadAppliedRecord(ctx, view.Binding)
		if err != nil {
			return PublicationRestorationMaterial{}, err
		}
		if a.Record.ID != view.Snapshot || a.Record.Commit != view.Commit {
			return PublicationRestorationMaterial{}, result.Fail("PLAN_STALE", "恢复视图的原应用归档在审查期间变化", 3)
		}
		out.views[i] = a
	}
	return out, nil
}

// Read only this member's exact ownership file from its original common
// backup namespace. Native bytes never pass through a source/replay cache.
func (m *Manager) publicationRestorationLedger(blobs *workstore.Store, images workstore.BatchPlan, after bool) (connectionLedger, error) {
	for _, target := range images.Targets {
		if target.Path != "native/"+connectionLedgerPath {
			continue
		}
		tree := target.Before
		if after {
			tree = target.After
		}
		if !tree.Exists {
			return connectionLedger{Tools: map[string]connectionTool{}}, nil
		}
		body, err := blobs.Data(tree.Entries[workstore.FileContent])
		if err != nil {
			return connectionLedger{}, err
		}
		return m.decodeConnectionLedger(body)
	}
	return connectionLedger{}, result.Fail("PUBLICATION_RESTORE_INVALID", "共同恢复缺少此成员的原生归属镜像", 5)
}

// Recheck is an observation for a preview, never a promise that authority will
// remain current. It checks all roots before/after, current permissions and
// the real commit of every reintroduced view; Apply still needs Validate under
// the complete common lock set on both sides of the write.
func (p PublicationRestorationMaterial) Recheck(ctx context.Context) error {
	if p.manager == nil {
		return result.Fail("PUBLICATION_RESTORE_INVALID", "缺少原成员恢复审查材料", 5)
	}
	m := p.manager
	if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
		return err
	}
	if err := p.originalCommitted(); err != nil {
		return err
	}
	c, err := p.proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		return err
	}
	if err = c.ObservePlan(p.plan.ID, p.plan.Digest); err != nil {
		return err
	}
	for _, a := range p.views {
		err = m.Source.WithFrozenMCPClientConfigs(ctx, a.Frozen, a.Plan.Request.Sessions, a.Plan.Executable, a.Plan.Home, func([]source.MCPClientConfiguration) error {
			return m.withAppliedCommit(ctx, a, func() error { return m.appliedArchiveCurrent(ctx, a) })
		})
		if err != nil {
			return err
		}
	}
	if err = c.ObservePlan(p.plan.ID, p.plan.Digest); err != nil {
		return err
	}
	return p.originalCommitted()
}

func (p PublicationRestorationMaterial) originalCommitted() error {
	state, err := p.proof.State(p.manager.Source.Store.Local)
	if err != nil {
		return err
	}
	if state != "committed" {
		return result.Fail("APPLIED_VIEW_NOT_COMMITTED", "原真实副本全批提交不可用，不能恢复应用视图", 4)
	}
	return nil
}

// Called only while the owner holds every common source/Local/native root
// lock and the corresponding library locks. Current authority is checked both
// before writing and before the actual shared commit. No lock is reacquired.
func (p PublicationRestorationMaterial) Validate(ctx context.Context, after bool) error {
	if p.manager == nil {
		return result.Fail("PUBLICATION_RESTORE_INVALID", "缺少原成员恢复审查材料", 5)
	}
	m := p.manager
	if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
		return err
	}
	if err := p.originalCommitted(); err != nil {
		return err
	}
	c, _ := p.proof.Coordinator(m.Source.Store.Local)
	stored, err := c.LoadPlan(p.plan.ID)
	if err != nil {
		return err
	}
	if string(jsonData(stored)) != string(jsonData(p.plan)) {
		return result.Fail("PLAN_STALE", "真实共同恢复提案在确认前变化", 3)
	}
	currentSource, currentLocal := replayApplicationTrees(snapshotReplayApplication{Transaction: p.member}, after)
	for _, a := range p.views {
		if err = m.appliedCommitLocked(a); err != nil {
			return err
		}
		if err = m.appliedArchiveCurrent(ctx, a); err != nil {
			return err
		}
		if err = m.Source.ValidateFrozenMCPTransaction(ctx, a.Frozen, currentSource, currentLocal, a.Plan.Request.Sessions); err != nil {
			return err
		}
	}
	return nil
}
