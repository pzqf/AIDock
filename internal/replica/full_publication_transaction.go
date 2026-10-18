package replica

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// The shared CLI/API owner consumes only a saved complete preview and exact
// confirmation digest. Member archives never substitute for its common proof.
type FullPublicationExecution struct {
	ID                    string                       `json:"publication_id"`
	Owner                 string                       `json:"project"`
	Scope                 string                       `json:"scope"`
	InitiatorRoot         string                       `json:"initiator_root"`
	Source                Binding                      `json:"source"`
	Members               []Binding                    `json:"members"`
	Status                string                       `json:"status"`
	Pending               bool                         `json:"pending"`
	Transactions          []FullPublicationTransaction `json:"transactions"`
	LatestTransaction     string                       `json:"latest_transaction,omitempty"`
	LatestAction          string                       `json:"latest_action,omitempty"`
	LatestMembers         []Binding                    `json:"latest_members"`
	UnchangedMembers      []Binding                    `json:"unchanged_members"`
	LatestSelectedMembers []Binding                    `json:"latest_selected_members"`
	LatestSelectionKnown  bool                         `json:"latest_selection_known"`
	Boundary              string                       `json:"boundary"`
}

type FullPublicationTransaction struct {
	Transaction
	SelectedMembers []Binding `json:"selected_members"`
	SelectionKnown  bool      `json:"selection_known"`
}

// The original roster, explicit selection and actual before/after footprint
// are distinct. Selected members may already match the requested old state.
func (m *Manager) fullPublicationSelection(p fullPublicationPlan, tx workstore.BatchPlan) ([]Binding, bool, error) {
	if tx.ID == p.Proof.Transaction.ID {
		return fullPublicationBindings(p), true, nil
	}
	raw, err := workstore.ReadFile(m.fullRestorationFile(p.ID, tx.ID))
	if os.IsNotExist(err) {
		return []Binding{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var header fullPublicationRestoration
	if decode(raw, &header) != nil {
		return nil, false, result.Fail("FULL_PUBLICATION_INVALID", "共同事务的明确成员记录不可读", 5)
	}
	r, _, err := m.loadFullRestoration(p, tx.ID, header.Digest)
	if err != nil {
		return nil, false, err
	}
	out := []Binding{}
	for _, b := range fullPublicationBindings(p) {
		if containsCopy(r.Copies, b.ID) {
			out = append(out, b)
		}
	}
	return out, true, nil
}

type fullPublicationArchive struct {
	Copy    string `json:"copy"`
	ID      string `json:"snapshot"`
	Commit  string `json:"commit"`
	Binding string `json:"applied_binding"`
}

func (m *Manager) fullPublicationCoordinator(p fullPublicationPlan) (*workstore.Coordinator, error) {
	c, err := p.Proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		return nil, err
	}
	c.Hook = m.PublicationHook
	c.RecoveryCommand = "replica publication full-recover --publication " + p.ID
	// Reuse only the established identity policy, never an independent
	// publication transaction or synthetic member success record.
	identity := PublicationPlan{Owner: p.Resolution.Owner, Scope: p.Resolution.Scope, Resolution: p.Resolution}
	c.RecoveryValidate = func(tx workstore.BatchPlan) error { return m.publicationRecoveryIdentity(identity, tx) }
	return c, nil
}

func (m *Manager) fullPublicationMaterialsMatch(p fullPublicationPlan, materials []native.ConfigurationPublicationMaterial) error {
	if len(materials) != len(p.Proof.Members) {
		return result.Fail("FULL_PUBLICATION_INVALID", "完整批次缺少明确目标的编译材料", 5)
	}
	byPath := map[string]workstore.BatchTarget{}
	for _, target := range p.Proof.Transaction.Targets {
		byPath[target.Path] = target
	}
	for i, material := range materials {
		if material.Binding() != p.Proof.Members[i] {
			return result.Fail("FULL_PUBLICATION_INVALID", "完整编译材料与原批次成员不一致", 5)
		}
		for _, target := range material.Targets() {
			target.Path = projectsnapshot.PublicationMemberPrefix(material.Binding().Copy) + target.Path
			if string(data(target)) != string(data(byPath[target.Path])) {
				return result.Fail("PLAN_STALE", "完整批次的源、本机观察或原生材料在预览后变化", 3)
			}
		}
	}
	return nil
}

func (m *Manager) fullPublicationSourceManagers(p fullPublicationPlan) (*source.Manager, []*source.Manager, []string, error) {
	publisher, err := m.copySource(p.Resolution.Source)
	if err != nil {
		return nil, nil, nil, err
	}
	managers := []*source.Manager{publisher}
	locals := []string{}
	for _, t := range p.Resolution.Targets {
		s, err := m.copySource(t.Copy)
		if err != nil {
			return nil, nil, nil, err
		}
		managers = append(managers, s)
		locals = append(locals, source.LocalProfileRoot(m.Source.Store.Local, p.Resolution.Owner, p.Resolution.Scope, t.Copy.ID))
	}
	return publisher, managers, locals, nil
}

func (m *Manager) ApplyFullPublication(ctx context.Context, id, digest string) (FullPublicationExecution, error) {
	p, err := m.loadFullPublication(id, digest)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	if digest == "" {
		return FullPublicationExecution{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要原完整批次预览的完整摘要", 4)
	}
	materials, _, err := m.compileFullPublication(ctx, p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	if err = m.fullPublicationMaterialsMatch(p, materials); err != nil {
		return FullPublicationExecution{}, err
	}
	publisher, managers, locals, err := m.fullPublicationSourceManagers(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	var sourceBefore workstore.Tree
	var configurationBefore string
	err = publisher.Store.Locked(func() error {
		return source.WithConfigurationLocks([]*source.Manager{publisher}, func() error {
			var err error
			sourceBefore, err = publisher.Store.Capture(publisher.Store.Root)
			if err == nil {
				configurationBefore, err = publisher.ConfigurationDigest()
			}
			return err
		})
	})
	if err != nil {
		return FullPublicationExecution{}, err
	}
	// Full reconstruction happens outside all-root locks. The captured
	// publisher bytes/layers and every compiled member are rechecked inside.
	if err = m.revalidate(ctx, p.Resolution); err != nil {
		return FullPublicationExecution{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	archives := []fullPublicationArchive{}
	for _, material := range materials {
		r, err := material.Capture(ctx, p.Proof)
		if err != nil {
			return FullPublicationExecution{}, err
		}
		archives = append(archives, fullPublicationArchive{Copy: r.Binding.Copy, ID: r.ID, Commit: r.Commit, Binding: r.ActiveKey})
	}
	if err = saveLocal(filepath.Join(c.Local, "archive-intents.json"), archives); err != nil {
		return FullPublicationExecution{}, err
	}
	check := func(after bool, tx workstore.BatchPlan) error {
		stored, err := m.loadFullPublication(id, digest)
		if err != nil {
			return err
		}
		if stored.Digest != p.Digest || string(data(tx)) != string(data(p.Proof.Transaction)) {
			return result.Fail("PLAN_STALE", "完整批次提案或真实共同事务在确认前变化", 3)
		}
		current, err := publisher.Store.Capture(publisher.Store.Root)
		if err != nil {
			return err
		}
		configuration, err := publisher.ConfigurationDigest()
		if err != nil {
			return err
		}
		if workstore.Digest(current) != workstore.Digest(sourceBefore) || configuration != configurationBefore {
			return result.Fail("PLAN_STALE", "只读发布者的源或配置层在完整提交期间变化", 3)
		}
		for _, b := range append([]Binding{p.Resolution.Source}, fullPublicationBindings(p)...) {
			if branch(b.Root) != b.Branch {
				return result.Fail("PLAN_STALE", "完整批次成员的分支在提交期间变化", 3)
			}
		}
		for _, material := range materials {
			if err = material.Validate(ctx, after); err != nil {
				return err
			}
		}
		return nil
	}
	c.Validate = func(tx workstore.BatchPlan) error { return check(false, tx) }
	c.BeforeCommit = func(tx workstore.BatchPlan) error { return check(true, tx) }
	err = publisher.Store.Locked(func() error {
		return source.WithConfigurationTransactionLocks(managers, locals, func() error {
			_, err := c.Apply(ctx, p.Proof.Transaction.ID, p.Proof.Transaction.Digest)
			return err
		})
	})
	if err != nil {
		return FullPublicationExecution{}, err
	}
	return m.fullPublicationExecution(p)
}

func fullPublicationBindings(p fullPublicationPlan) []Binding {
	out := []Binding{}
	for _, t := range p.Resolution.Targets {
		out = append(out, t.Copy)
	}
	return out
}

func (m *Manager) fullPublicationExecution(p fullPublicationPlan) (FullPublicationExecution, error) {
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	js, err := c.History()
	if err != nil {
		return FullPublicationExecution{}, err
	}
	out := FullPublicationExecution{ID: p.ID, Owner: p.Resolution.Owner, Scope: p.Resolution.Scope, InitiatorRoot: p.InitiatorRoot, Source: p.Resolution.Source, Members: fullPublicationBindings(p), Status: "SAVED", Transactions: []FullPublicationTransaction{}, LatestMembers: []Binding{}, UnchangedMembers: []Binding{}, LatestSelectedMembers: []Binding{}, Boundary: "真实共同事务分别记录原批次成员、明确选择和计划变化；已选择成员可能已经与目标状态一致。未完成事务的变化不是已提交结果，恢复后的计划不是当前应用。发布者只读、本机覆盖保留，不回放旧权限、会话、审计或密钥。文件提交不代表工具已运行。"}
	for _, j := range js {
		pending, err := c.MarkersPending(j.Plan.ID)
		if err != nil {
			return FullPublicationExecution{}, err
		}
		out.Pending = out.Pending || j.State == "prepared" || pending
		selection, known, err := m.fullPublicationSelection(p, j.Plan)
		if err != nil {
			return FullPublicationExecution{}, err
		}
		t := FullPublicationTransaction{Transaction: Transaction{ID: j.Plan.ID, Action: j.Plan.Action, Reference: j.Plan.Reference, State: j.State, Updated: j.Updated}, SelectedMembers: selection, SelectionKnown: known}
		members := map[string]bool{}
		for _, target := range j.Plan.Targets {
			n := len(workstore.Changes(target.Before, target.After))
			t.Changes += n
			if workstore.Digest(target.Before) == workstore.Digest(target.After) || target.Path == "control" {
				continue
			}
			parts := strings.Split(target.Path, "/")
			if len(parts) > 2 {
				members[parts[1]] = true
			}
			if strings.Contains(target.Path, "/native/") {
				t.Generated++
				if strings.HasSuffix(target.Path, "/CLAUDE.md") || strings.HasSuffix(target.Path, "/AGENTS.md") {
					t.RuleFiles++
				}
			}
		}
		t.Targets = len(members)
		if len(out.Transactions) == 0 {
			out.LatestTransaction, out.LatestAction = j.Plan.ID, j.Plan.Action
			out.LatestSelectedMembers, out.LatestSelectionKnown = selection, known
			for _, b := range out.Members {
				if members[b.ID] {
					out.LatestMembers = append(out.LatestMembers, b)
				} else {
					out.UnchangedMembers = append(out.UnchangedMembers, b)
				}
			}
		}
		out.Transactions = append(out.Transactions, t)
	}
	if out.Pending {
		out.Status = "PENDING"
	} else if len(js) > 0 {
		if js[0].State == "recovered" {
			out.Status = "RECOVERED"
		} else {
			out.Status = map[string]string{"apply": "APPLIED", "rollback": "ROLLED_BACK", "undo-rollback": "UNDONE"}[js[0].Plan.Action]
			if out.Status == "" {
				out.Status = "UNKNOWN"
			}
		}
	}
	return out, nil
}

func (m *Manager) InspectFullPublicationRecovery(id string) (workstore.RecoveryInspection, error) {
	p, err := m.loadFullPublication(id, "")
	if err != nil {
		return workstore.RecoveryInspection{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return workstore.RecoveryInspection{}, err
	}
	return c.InspectRecovery()
}

func (m *Manager) RecoverFullPublication(id string) (FullPublicationExecution, error) {
	p, err := m.loadFullPublication(id, "")
	if err != nil {
		return FullPublicationExecution{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	if _, err = c.Recover(); err != nil {
		return FullPublicationExecution{}, err
	}
	return m.fullPublicationExecution(p)
}
