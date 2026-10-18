package replica

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type fullPublicationRestoration struct {
	Version     int                          `json:"schema_version"`
	Publication string                       `json:"publication_id"`
	Original    string                       `json:"original_transaction"`
	OriginalSHA string                       `json:"original_digest"`
	Copies      []string                     `json:"copies"`
	Transaction workstore.BatchPlan          `json:"transaction"`
	Digest      string                       `json:"digest"`
	Regenerated *fullPublicationRegeneration `json:"regenerated,omitempty"`
}

type FullPublicationRestorationMember struct {
	Copy  Binding                             `json:"copy"`
	Views []native.PublicationRestorationView `json:"views"`
	Code  string                              `json:"code,omitempty"`
}

type FullPublicationRestorationPreview struct {
	ID          string                             `json:"id"`
	Publication string                             `json:"publication_id"`
	Original    string                             `json:"original_transaction"`
	Digest      string                             `json:"digest"`
	Action      string                             `json:"action"`
	Members     []FullPublicationRestorationMember `json:"members"`
	Retained    []Binding                          `json:"retained_members"`
	Changes     []PublicationReplayChange          `json:"changes"`
	CanApply    bool                               `json:"can_apply"`
	Code        string                             `json:"code,omitempty"`
	Boundary    string                             `json:"boundary"`
	Regenerated bool                               `json:"regenerated"`
}

func fullRestorationDigest(p fullPublicationRestoration) string {
	p.Digest = ""
	return workstore.Hash(data(p))
}

func (m *Manager) fullRestorationFile(publication, id string) string {
	return filepath.Join(projectsnapshot.PublicationArea(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, publication), "restorations", id+".json")
}

// Explicit copies are mandatory. Keep current Local layers and immutable
// source histories, and restore only the selected source/native changes and
// the corresponding pairwise baselines. No user-supplied paths reach the kernel.
func (m *Manager) fullRestorationSelection(p fullPublicationPlan, original workstore.BatchPlan, copies []string) ([]string, map[string][]string, error) {
	if err := m.validateFullPublication(p); err != nil {
		return nil, nil, err
	}
	c, _ := p.Proof.Coordinator(m.Source.Store.Local)
	if err := c.ValidateManifest(original); err != nil {
		return nil, nil, err
	}
	if len(copies) == 0 || len(copies) > len(p.Proof.Members) {
		return nil, nil, result.Fail("REPLAY_COPY_SELECTION_INVALID", "明确选择至少一个原批次目标；发布者不作为恢复目标", 2)
	}
	available := map[string]bool{}
	for _, b := range p.Proof.Members {
		available[b.Copy] = true
	}
	ids := append([]string{}, copies...)
	sort.Strings(ids)
	selection := map[string][]string{"control": {}}
	for i, id := range ids {
		if !available[id] || i > 0 && ids[i-1] == id {
			return nil, nil, result.Fail("REPLAY_COPY_SELECTION_INVALID", "恢复成员重复或不属于原完整批次", 2)
		}
		selection["control"] = append(selection["control"], pairPath(p.Resolution.Source.ID, id))
		prefix := projectsnapshot.PublicationMemberPrefix(id)
		for _, target := range original.Targets {
			if strings.HasPrefix(target.Path, prefix+"native/") {
				selection[target.Path] = nil
			}
			if target.Path != prefix+"source" {
				continue
			}
			paths := []string{}
			for _, change := range workstore.Changes(target.Before, target.After) {
				immutable := false
				for _, protected := range []string{".revisions", ".revision-records", ".governance-records", ".external-approvals"} {
					immutable = immutable || change.Path == protected || strings.HasPrefix(change.Path, protected+"/")
				}
				if !immutable {
					paths = append(paths, change.Path)
				}
			}
			if len(paths) > 0 {
				selection[target.Path] = paths
			}
		}
	}
	return ids, selection, nil
}

func (m *Manager) fullRestorationMaterials(ctx context.Context, p fullPublicationPlan, tx workstore.BatchPlan, copies []string) ([]native.PublicationRestorationMaterial, []FullPublicationRestorationMember, error) {
	materials := []native.PublicationRestorationMaterial{}
	members := []FullPublicationRestorationMember{}
	var firstError error
	for _, b := range fullPublicationBindings(p) {
		if !containsCopy(copies, b.ID) {
			continue
		}
		member := FullPublicationRestorationMember{Copy: b, Views: []native.PublicationRestorationView{}}
		err := func() error {
			s, err := m.copySource(b)
			if err != nil {
				return err
			}
			n, err := native.New(s, filepath.Dir(b.Root))
			if err != nil {
				return err
			}
			material, err := n.InspectPublicationRestoration(ctx, p.Proof, tx.ID, tx.Digest)
			if err != nil {
				return err
			}
			member.Views = material.Views()
			if err = material.Recheck(ctx); err != nil {
				return err
			}
			materials = append(materials, material)
			return nil
		}()
		if err != nil {
			member.Code = "FULL_PUBLICATION_RESTORE_REVIEW_REQUIRED"
			exit := 4
			var failure *result.Error
			if errors.As(err, &failure) {
				member.Code, exit = failure.Code, failure.Exit
			}
			if firstError == nil {
				firstError = result.Fail(member.Code, "完整批次恢复目标 "+b.ID+" 需要核对："+member.Code, exit)
			}
		}
		members = append(members, member)
	}
	return materials, members, firstError
}

func (m *Manager) fullRestorationIdentity(ctx context.Context, p fullPublicationPlan, copies []string) error {
	tree, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		return err
	}
	registry, err := m.registry(tree)
	if err != nil {
		return err
	}
	identity := PublicationPlan{Owner: p.Resolution.Owner, Scope: p.Resolution.Scope}
	for _, b := range fullPublicationBindings(p) {
		if !containsCopy(copies, b.ID) {
			continue
		}
		active := false
		for _, registered := range registry.Copies {
			active = active || registered.ID == b.ID && registered.Root == b.Root
		}
		if !active || branch(b.Root) != b.Branch {
			return result.Fail("PLAN_STALE", "所选恢复成员已移除、重绑或切换分支", 3)
		}
		if err = m.publicationCopyIdentity(ctx, identity, b, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) PlanFullPublicationRestoration(ctx context.Context, id, original string, copies []string) (FullPublicationRestorationPreview, error) {
	p, err := m.loadFullPublication(id, "")
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	if original == p.ID {
		original = p.Proof.Transaction.ID
	}
	js, err := c.History()
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	var node workstore.BatchPlan
	for _, j := range js {
		if j.Plan.ID == original && j.State == "committed" {
			node = j.Plan
			break
		}
	}
	if node.ID == "" {
		return FullPublicationRestorationPreview{}, result.Fail("TRANSACTION_NOT_FOUND", "恢复节点不属于此完整批次的真实成功事务", 2)
	}
	ids, selection, err := m.fullRestorationSelection(p, node, copies)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	tx, err := c.RestoreSelected(node.ID, selection)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	if err = retainFullRestorationImages(c, tx); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	r := fullPublicationRestoration{Version: 1, Publication: p.ID, Original: node.ID, OriginalSHA: node.Digest, Copies: ids, Transaction: tx}
	r.Digest = fullRestorationDigest(r)
	if err = saveLocal(m.fullRestorationFile(p.ID, tx.ID), r); err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	return m.ReadFullPublicationRestoration(ctx, p.ID, tx.ID, r.Digest)
}

func (m *Manager) loadFullRestoration(p fullPublicationPlan, id, digest string) (fullPublicationRestoration, map[string][]string, error) {
	return m.loadFullRestorationLevel(p, id, digest, true)
}

func (m *Manager) loadFullRestorationLevel(p fullPublicationPlan, id, digest string, allowRegeneration bool) (fullPublicationRestoration, map[string][]string, error) {
	var r fullPublicationRestoration
	if !uuid.MatchString(id) || !fullPublicationSHA.MatchString(digest) {
		return r, nil, result.Fail("INVALID_INPUT", "需要完整批次恢复提案 UUID 和完整摘要", 2)
	}
	body, err := workstore.ReadFile(m.fullRestorationFile(p.ID, id))
	if err != nil {
		return r, nil, err
	}
	if decode(body, &r) != nil || r.Version != 1 || r.Publication != p.ID || r.Transaction.ID != id || r.Digest != digest || fullRestorationDigest(r) != digest || !uuid.MatchString(r.Original) || !fullPublicationSHA.MatchString(r.OriginalSHA) {
		return r, nil, result.Fail("FULL_PUBLICATION_INVALID", "完整批次恢复提案的身份或摘要不符", 5)
	}
	c, _ := m.fullPublicationCoordinator(p)
	original, err := c.LoadPlan(r.Original)
	if err != nil {
		return r, nil, err
	}
	ids, selection, err := m.fullRestorationSelection(p, original, r.Copies)
	if err != nil || string(data(ids)) != string(data(r.Copies)) {
		return r, nil, result.Fail("FULL_PUBLICATION_INVALID", "完整批次恢复成员选择不符", 5)
	}
	stored, err := c.LoadPlan(id)
	if err != nil {
		return r, nil, err
	}
	if string(data(stored)) != string(data(r.Transaction)) {
		return r, nil, result.Fail("FULL_PUBLICATION_INVALID", "恢复提案与真实共同事务不符", 5)
	}
	role := r.Transaction
	if r.Regenerated != nil {
		if !allowRegeneration {
			return r, nil, result.Fail("FULL_PUBLICATION_INVALID", "重新生成必须直接引用原完整恢复提案，不能循环或嵌套", 5)
		}
		seed, _, err := m.loadFullRestorationLevel(p, r.Regenerated.Seed, r.Regenerated.SeedSHA, false)
		if err != nil {
			return r, nil, err
		}
		if err = m.validateFullRegeneratedRole(seed, r); err != nil {
			return r, nil, err
		}
		role = seed.Transaction
	}
	if err = c.CheckRestoration(r.Original, r.OriginalSHA, selection, role); err != nil {
		return r, nil, err
	}
	return r, selection, nil
}

func (m *Manager) ReadFullPublicationRestoration(ctx context.Context, publication, id, digest string) (FullPublicationRestorationPreview, error) {
	p, err := m.loadFullPublication(publication, "")
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	r, _, err := m.loadFullRestoration(p, id, digest)
	if err != nil {
		return FullPublicationRestorationPreview{}, err
	}
	out := FullPublicationRestorationPreview{ID: id, Publication: p.ID, Original: r.Original, Digest: digest, Action: r.Transaction.Action, Members: []FullPublicationRestorationMember{}, Retained: []Binding{}, Changes: []PublicationReplayChange{}, Boundary: "只恢复明确选择成员的源、工具原生变化和对应共同基线；当前本机层、不可变版本历史、发布者与未选成员保留。不恢复旧授权；重新引入的应用须有真实提交和当前权限。缺少条件时必须明确审查并重新生成参数，不能用旧记录冒充当前授权。"}
	out.Regenerated = r.Regenerated != nil
	for _, b := range fullPublicationBindings(p) {
		if !containsCopy(r.Copies, b.ID) {
			out.Retained = append(out.Retained, b)
		} else {
			out.Members = append(out.Members, FullPublicationRestorationMember{Copy: b, Views: []native.PublicationRestorationView{}})
		}
	}
	if err = m.fullRestorationUnconsumed(p, r); err != nil {
		out.Code = "PLAN_CONSUMED"
		return out, nil
	}
	for _, t := range r.Transaction.Targets {
		out.Changes = append(out.Changes, PublicationReplayChange{Path: t.Path, Changes: workstore.Changes(t.Before, t.After)})
	}
	if err = m.fullRestorationIdentity(ctx, p, r.Copies); err != nil {
		out.Code = "PLAN_STALE"
		return out, nil
	}
	_, _, members, err := m.reviewFullRestoration(ctx, p, r)
	out.Members = members
	if err != nil {
		out.Code = "FULL_PUBLICATION_RESTORE_REVIEW_REQUIRED"
		var failure *result.Error
		if errors.As(err, &failure) {
			out.Code = failure.Code
		}
		return out, nil
	}
	out.CanApply = true
	return out, nil
}

func (m *Manager) ApplyFullPublicationRestoration(ctx context.Context, publication, id, digest string) (FullPublicationExecution, error) {
	p, err := m.loadFullPublication(publication, "")
	if err != nil {
		return FullPublicationExecution{}, err
	}
	r, selection, err := m.loadFullRestoration(p, id, digest)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	if err = m.fullRestorationUnconsumed(p, r); err != nil {
		return FullPublicationExecution{}, err
	}
	materials, regenerated, _, err := m.reviewFullRestoration(ctx, p, r)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	publisher, managers, locals, err := m.fullPublicationSourceManagers(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	before, err := publisher.Store.Capture(publisher.Store.Root)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return FullPublicationExecution{}, err
	}
	actual := p.Proof
	actual.Transaction = r.Transaction
	archives := []fullPublicationArchive{}
	for _, material := range regenerated {
		records, err := material.Capture(ctx, actual)
		if err != nil {
			return FullPublicationExecution{}, err
		}
		for _, record := range records {
			archives = append(archives, fullPublicationArchive{Copy: record.Binding.Copy, ID: record.ID, Commit: record.Commit, Binding: record.ActiveKey})
		}
	}
	if len(archives) > 0 {
		if err = saveLocal(filepath.Join(c.Local, "restoration-archive-intents", id+".json"), archives); err != nil {
			return FullPublicationExecution{}, err
		}
	}
	check := func(after bool, tx workstore.BatchPlan) error {
		stored, _, err := m.loadFullRestoration(p, id, digest)
		if err != nil {
			return err
		}
		if stored.Digest != r.Digest || string(data(tx)) != string(data(r.Transaction)) {
			return result.Fail("PLAN_STALE", "完整批次恢复提案变化", 3)
		}
		role := tx
		if r.Regenerated != nil {
			seed, _, err := m.loadFullRestorationLevel(p, r.Regenerated.Seed, r.Regenerated.SeedSHA, false)
			if err != nil {
				return err
			}
			role = seed.Transaction
		}
		if err = c.CheckRestoration(r.Original, r.OriginalSHA, selection, role); err != nil {
			return err
		}
		current, err := publisher.Store.Capture(publisher.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(current) != workstore.Digest(before) {
			return result.Fail("PLAN_STALE", "只读发布者在完整批次恢复期间变化", 3)
		}
		if err = m.fullRestorationIdentity(ctx, p, r.Copies); err != nil {
			return err
		}
		for _, material := range materials {
			if err = material.Validate(ctx, after); err != nil {
				return err
			}
		}
		for _, material := range regenerated {
			if err = material.Validate(ctx, actual, after); err != nil {
				return err
			}
		}
		return nil
	}
	c.Validate = func(tx workstore.BatchPlan) error { return check(false, tx) }
	c.BeforeCommit = func(tx workstore.BatchPlan) error { return check(true, tx) }
	err = publisher.Store.Locked(func() error {
		return source.WithConfigurationTransactionLocks(managers, locals, func() error {
			_, err := c.Apply(ctx, id, r.Transaction.Digest)
			return err
		})
	})
	if err != nil {
		return FullPublicationExecution{}, err
	}
	return m.fullPublicationExecution(p)
}

func (m *Manager) fullRestorationUnconsumed(p fullPublicationPlan, r fullPublicationRestoration) error {
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		return err
	}
	js, err := c.History()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.Plan.ID == r.Transaction.ID {
			return result.Fail("PLAN_CONSUMED", "此共同恢复提案已执行；请读取真实事务，不能重复确认", 3)
		}
	}
	return nil
}
