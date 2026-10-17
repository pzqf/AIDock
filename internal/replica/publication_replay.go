package replica

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type PublicationSnapshot struct {
	Publication string    `json:"publication_id"`
	Snapshot    string    `json:"snapshot_id"`
	Transaction string    `json:"transaction"`
	State       string    `json:"state"`
	Copies      []Binding `json:"copies"`
}

// The publication UUID denotes its initial before-apply recovery node. Every
// successful later rollback/undo transaction is also an explicit recovery node.
// No ID is guessed from directory names, current ordering or a different copy.
func (m *Manager) FindPublicationSnapshot(id string) (PublicationSnapshot, bool, error) {
	var out PublicationSnapshot
	if !uuid.MatchString(id) {
		return out, false, result.Fail("INVALID_INPUT", "恢复节点需要完整 UUID", 2)
	}
	rows, err := m.PublicationHistory()
	if err != nil {
		return out, false, err
	}
	found := false
	for _, row := range rows {
		p, err := m.loadPublication(row.ID)
		if err != nil {
			return out, false, err
		}
		for _, tx := range row.Transactions {
			if id != tx.ID && !(id == row.ID && tx.ID == p.Transaction.ID) {
				continue
			}
			if found {
				return out, false, result.Fail("SNAPSHOT_AMBIGUOUS", "恢复节点匹配多个副本批次，不能自动选择", 5)
			}
			out = PublicationSnapshot{Publication: p.ID, Snapshot: id, Transaction: tx.ID, State: tx.State, Copies: []Binding{}}
			for _, target := range p.Resolution.Targets {
				out.Copies = append(out.Copies, target.Copy)
			}
			found = true
		}
	}
	return out, found, nil
}

type publicationReplay struct {
	Version     int                 `json:"schema_version"`
	Publication string              `json:"publication_id"`
	Snapshot    string              `json:"snapshot_id"`
	Original    string              `json:"original_transaction"`
	OriginalSHA string              `json:"original_digest"`
	Copies      []string            `json:"copies"`
	Transaction workstore.BatchPlan `json:"transaction"`
	Digest      string              `json:"digest"`
}

type PublicationReplayChange struct {
	Path    string             `json:"path"`
	Changes []workstore.Change `json:"changes"`
}

type PublicationReplayPreview struct {
	ID          string                    `json:"id"`
	Publication string                    `json:"publication_id"`
	Snapshot    string                    `json:"snapshot_id"`
	Action      string                    `json:"action"`
	Digest      string                    `json:"digest"`
	Owner       string                    `json:"project"`
	Scope       string                    `json:"scope"`
	Copies      []Binding                 `json:"copies"`
	Retained    []Binding                 `json:"retained_copies"`
	Changes     []PublicationReplayChange `json:"changes"`
	CanApply    bool                      `json:"can_apply"`
	Code        string                    `json:"code,omitempty"`
	Boundary    string                    `json:"boundary"`
}

func publicationReplayDigest(p publicationReplay) string {
	p.Digest = ""
	return workstore.Hash(data(p))
}
func (m *Manager) publicationReplayFile(publication, id string) string {
	return filepath.Join(m.publicationsDir(), "replays", publication, id+".json")
}

// Selection is derived only from the saved batch membership. In particular,
// a publisher, another scope, a removed member or an arbitrary filesystem root
// cannot become a recovery destination through the public copies parameter.
func publicationReplaySelection(p PublicationPlan, copies []string) ([]string, map[string][]string, error) {
	available := map[string]bool{}
	for _, target := range p.Resolution.Targets {
		available[target.Copy.ID] = true
	}
	selected := map[string]bool{}
	if copies == nil {
		for id := range available {
			selected[id] = true
		}
	} else {
		if len(copies) == 0 {
			return nil, nil, result.Fail("INVALID_INPUT", "copies 必须明确选择至少一个批次目标", 2)
		}
		for _, id := range copies {
			if !uuid.MatchString(id) || !available[id] || selected[id] {
				return nil, nil, result.Fail("REPLAY_COPY_SELECTION_INVALID", "副本选择重复或不属于原批次的目标；发布者不作为恢复目标", 2)
			}
			selected[id] = true
		}
	}
	ids := []string{}
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == len(available) {
		return ids, nil, nil
	}
	selection := map[string][]string{"control": {}}
	for _, id := range ids {
		selection["control"] = append(selection["control"], pairPath(p.Resolution.Source.ID, id))
		for _, target := range p.Transaction.Targets {
			if strings.HasPrefix(target.Path, "copies/"+id+"/") {
				selection[target.Path] = nil
			}
		}
	}
	return ids, selection, nil
}

func (m *Manager) loadPublicationReplay(p PublicationPlan, id, digest string) (publicationReplay, error) {
	var r publicationReplay
	if !uuid.MatchString(id) || len(digest) != 64 {
		return r, result.Fail("INVALID_INPUT", "副本回放需要提案 UUID 与完整摘要", 2)
	}
	body, err := workstore.ReadFile(m.publicationReplayFile(p.ID, id))
	if err != nil {
		return r, err
	}
	if decode(body, &r) != nil || r.Version != 1 || r.Publication != p.ID || r.Transaction.ID != id || r.Digest != digest || r.Digest != publicationReplayDigest(r) || !uuid.MatchString(r.Snapshot) || !uuid.MatchString(r.Original) || len(r.OriginalSHA) != 64 || (r.Transaction.Action != "rollback" && r.Transaction.Action != "undo-rollback") {
		return r, result.Fail("BACKUP_INVALID", "副本回放提案的完整绑定或摘要不一致", 5)
	}
	if r.Snapshot != r.Original && !(r.Snapshot == p.ID && r.Original == p.Transaction.ID) {
		return r, result.Fail("BACKUP_INVALID", "副本回放节点与原成功事务不对应", 5)
	}
	ids, _, err := publicationReplaySelection(p, r.Copies)
	if err != nil || string(data(ids)) != string(data(r.Copies)) {
		return r, result.Fail("BACKUP_INVALID", "副本回放选择与原批次不一致", 5)
	}
	actual, err := m.publicationCoordinator(p).LoadPlan(id)
	if err != nil {
		return r, err
	}
	if string(data(actual)) != string(data(r.Transaction)) {
		return r, result.Fail("BACKUP_INVALID", "副本回放提案与完整共同事务不一致", 5)
	}
	return r, nil
}

func (m *Manager) publicationReplayCoordinator(ctx context.Context, p PublicationPlan, r publicationReplay) *workstore.Coordinator {
	c := m.publicationCoordinator(p)
	_, selection, _ := publicationReplaySelection(p, r.Copies)
	validate := func(tx workstore.BatchPlan) error {
		stored, err := m.loadPublicationReplay(p, r.Transaction.ID, r.Digest)
		if err != nil {
			return err
		}
		if stored.Digest != r.Digest || string(data(tx)) != string(data(r.Transaction)) {
			return result.Fail("PLAN_STALE", "副本恢复提案已变化", 3)
		}
		if err = c.CheckRestoration(r.Original, r.OriginalSHA, selection, tx); err != nil {
			return err
		}
		control, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		registry, err := m.registry(control)
		if err != nil {
			return err
		}
		for _, target := range p.Resolution.Targets {
			if !containsCopy(r.Copies, target.Copy.ID) {
				continue
			}
			registered := false
			for _, current := range registry.Copies {
				registered = registered || current.ID == target.Copy.ID && current.Root == target.Copy.Root
			}
			if !registered {
				return result.Fail("REPLAY_COPY_SELECTION_INVALID", "恢复目标已移除、归档或重绑，不能自动写入旧目录", 3)
			}
			if branch(target.Copy.Root) != target.Copy.Branch {
				return result.Fail("PLAN_STALE", "恢复目标的分支已变化", 3)
			}
			if err = m.publicationCopyIdentity(ctx, p, target.Copy, false); err != nil {
				return err
			}
		}
		return nil
	}
	c.Validate, c.BeforeCommit = validate, validate
	return c
}

func containsCopy(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func publicationReplayPreview(p PublicationPlan, r publicationReplay) PublicationReplayPreview {
	out := PublicationReplayPreview{ID: r.Transaction.ID, Publication: p.ID, Snapshot: r.Snapshot, Action: r.Transaction.Action, Digest: r.Digest, Owner: p.Owner, Scope: p.Scope, Copies: []Binding{}, Retained: []Binding{}, Changes: []PublicationReplayChange{}, CanApply: true, Boundary: "恢复明确选择的原批次目标及对应共同基线；未选副本和发布者保留当前文件，本机覆盖、会话、审计和授权不回放。局部回退会形成共享差异，不自动传播。整个原批次仍共用根锁和中断恢复边界，全部根需可安全观察。确认前后复核实际字节及所选项目身份，失败整批恢复本次修改。"}
	for _, target := range p.Resolution.Targets {
		if containsCopy(r.Copies, target.Copy.ID) {
			out.Copies = append(out.Copies, target.Copy)
		} else {
			out.Retained = append(out.Retained, target.Copy)
		}
	}
	for _, t := range r.Transaction.Targets {
		if changes := workstore.Changes(t.Before, t.After); len(changes) > 0 {
			out.Changes = append(out.Changes, PublicationReplayChange{t.Path, changes})
		}
	}
	return out
}

func (m *Manager) PlanPublicationReplay(ctx context.Context, snapshot string, copies []string) (PublicationReplayPreview, error) {
	ref, found, err := m.FindPublicationSnapshot(snapshot)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	if !found || ref.State != "committed" {
		return PublicationReplayPreview{}, result.Fail("SNAPSHOT_NOT_COMMITTED", "副本恢复节点不存在或尚未成功提交", 4)
	}
	p, err := m.loadPublication(ref.Publication)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	ids, selection, err := publicationReplaySelection(p, copies)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	c := m.publicationCoordinator(p)
	js, err := c.History()
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	var original string
	for _, j := range js {
		if j.Plan.ID == ref.Transaction && j.State == "committed" {
			original = j.Plan.Digest
		}
	}
	tx, err := c.RestoreSelected(ref.Transaction, selection)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	r := publicationReplay{Version: 1, Publication: p.ID, Snapshot: snapshot, Original: ref.Transaction, OriginalSHA: original, Copies: ids, Transaction: tx}
	r.Digest = publicationReplayDigest(r)
	if err = saveLocal(m.publicationReplayFile(p.ID, tx.ID), r); err != nil {
		return PublicationReplayPreview{}, err
	}
	return m.ReadPublicationReplay(ctx, p.ID, tx.ID, r.Digest)
}

func (m *Manager) ReadPublicationReplay(ctx context.Context, publication, id, digest string) (PublicationReplayPreview, error) {
	p, err := m.loadPublication(publication)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	r, err := m.loadPublicationReplay(p, id, digest)
	if err != nil {
		return PublicationReplayPreview{}, err
	}
	out := publicationReplayPreview(p, r)
	if err = m.publicationReplayCoordinator(ctx, p, r).ObservePlan(id, r.Transaction.Digest); err != nil {
		out.CanApply, out.Code, out.Changes = false, "PLAN_STALE", []PublicationReplayChange{}
		var classified *result.Error
		if errors.As(err, &classified) {
			out.Code = classified.Code
		}
	}
	return out, nil
}

func (m *Manager) ApplyPublicationReplay(ctx context.Context, publication, id, digest string) (Publication, error) {
	p, err := m.loadPublication(publication)
	if err != nil {
		return Publication{}, err
	}
	r, err := m.loadPublicationReplay(p, id, digest)
	if err != nil {
		return Publication{}, err
	}
	if _, err = m.publicationReplayCoordinator(ctx, p, r).Apply(ctx, id, r.Transaction.Digest); err != nil {
		return Publication{}, err
	}
	return m.publicationReport(p)
}

// Legacy confirmation must not bypass the stronger selected-replay binding.
func (m *Manager) hasPublicationReplay(publication, id string) (bool, error) {
	if !uuid.MatchString(publication) || !uuid.MatchString(id) {
		return false, result.Fail("INVALID_INPUT", "需要正式批次及恢复提案 UUID", 2)
	}
	path := m.publicationReplayFile(publication, id)
	if err := workstore.CheckLinks(path); err != nil {
		return false, err
	}
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
