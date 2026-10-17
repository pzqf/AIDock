package replica

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type PublicationRequest struct {
	Generation  string `json:"generation_id"`
	Digest      string `json:"digest"`
	Check       string `json:"check_id"`
	CheckDigest string `json:"check_digest"`
}

type publicationInput struct {
	Copy          Binding `json:"copy"`
	Snapshot      string  `json:"snapshot"`
	Configuration string  `json:"configuration_digest"`
}

// PublicationPlan is distinct from a rehearsal and from diagnostic checks.
// Its complete recovery descriptor remains usable without those old files.
type PublicationPlan struct {
	Version       int                 `json:"schema_version"`
	Kind          string              `json:"kind"`
	ID            string              `json:"id"`
	Owner         string              `json:"owner"`
	Scope         string              `json:"scope_id"`
	InitiatorRoot string              `json:"initiator_root"`
	InitiatorCopy string              `json:"initiator_copy"`
	Created       string              `json:"created"`
	Resolution    Resolution          `json:"resolution"`
	Generation    Generation          `json:"generation"`
	Check         GenerationCheck     `json:"check"`
	Inputs        []publicationInput  `json:"inputs"`
	Transaction   workstore.BatchPlan `json:"transaction"`
	Digest        string              `json:"digest"`
	Boundary      string              `json:"boundary"`
}

type Publication struct {
	ID           string                  `json:"id"`
	Created      string                  `json:"created"`
	Status       string                  `json:"status"`
	Pending      bool                    `json:"pending"`
	Source       Binding                 `json:"source"`
	Members      []Binding               `json:"target_copies"`
	Roots        []workstore.RootBinding `json:"bound_roots"`
	Transactions []Transaction           `json:"transactions"`
	Warning      string                  `json:"warning,omitempty"`
	Boundary     string                  `json:"boundary"`
}

func publicationDigest(p PublicationPlan) string { p.Digest = ""; return workstore.Hash(data(p)) }
func (m *Manager) publicationsDir() string       { return filepath.Join(m.resolutionDir(), "publications") }
func (m *Manager) publicationFile(id string) string {
	return filepath.Join(m.publicationsDir(), "plans", id+".json")
}

func (m *Manager) publicationCoordinator(p PublicationPlan) *workstore.Coordinator {
	roots := []workstore.RootBinding{{Path: "control", Root: m.Control.Root}}
	for i, t := range p.Resolution.Targets {
		roots = append(roots, workstore.RootBinding{Path: "copies/" + t.Copy.ID + "/.aidock", Root: t.Copy.Root})
		project := filepath.Dir(t.Copy.Root)
		for _, o := range p.Generation.Targets[i].Outputs {
			// The fixed tool container is a transaction root, so its first
			// creation/removal is recoverable too. Only the skills subtree
			// differs; every other file is preserved byte for byte.
			container := filepath.Dir(o.Root)
			roots = append(roots, workstore.RootBinding{Path: generatedPath(t.Copy.ID, o.Adapter.ID), Root: container, NativeProject: project})
			if o.Rule != nil {
				roots = append(roots, workstore.RootBinding{Path: generatedRulePath(t.Copy.ID, o.Adapter.ID), Root: filepath.Join(project, o.Rule.Path), File: true, NativeProject: project})
			}
		}
	}
	return &workstore.Coordinator{Local: filepath.Join(m.publicationsDir(), "runs", p.ID), Identity: strings.Join([]string{"replica-publication", p.Owner, p.Scope, p.ID}, "/"), Roots: roots, Blobs: m.Control, Hook: m.PublicationHook, RecoveryCommand: "replica publication recover --publication " + p.ID, RecoveryValidate: func(tx workstore.BatchPlan) error { return m.publicationRecoveryIdentity(p, tx) }}
}

func (m *Manager) publicationQualification(p PublicationPlan) error {
	if resolutionChangesMCP(p.Resolution) {
		return result.Fail("PUBLICATION_MCP_CONFIGURATION_REQUIRED", "此候选修改 MCP 定义或精确锁；先完成完整 MCP 联合编译、固定应用视图与全批恢复，不能使用仅技能与规则的正式同步入口", 4)
	}
	g, r := p.Generation, p.Check
	if g.Generator != mirrorGenerator || g.Version != 5 || g.ApplicationScope != native.SkillsRulesScope || g.Blocked || g.NoChanges {
		return result.Fail("PUBLICATION_SCOPE_UNSUPPORTED", "正式同步仅接受明确选择的 owned 技能与规则候选；完整 Profile 与外部生成器另行验收", 4)
	}
	if r.Policy != inspectionPolicy || r.Generation != g.ID || r.GenerationDigest != g.Digest || r.Digest != generationCheckDigest(r) || r.ApplicationScope != g.ApplicationScope || len(r.Targets) != len(g.Targets) || !r.Probe || r.Stale {
		return result.Fail("PUBLICATION_CHECK_REQUIRED", "需要当前候选的逐副本发现与规则来源检查", 4)
	}
	for i, target := range g.Targets {
		check := r.Targets[i]
		if string(data(check.Copy)) != string(data(p.Resolution.Targets[i].Copy)) || check.Source != target.SourceAfter {
			return result.Fail("CHECK_CORRUPT", "发布检查的目标或候选源不一致", 5)
		}
		n, err := m.candidateNative(check.Copy)
		if err != nil {
			return err
		}
		if _, err = n.QualifyDraft(target.Outputs, target.SourceAfter, g.ApplicationScope, target.ExcludedChecks, check.Inspection); err != nil {
			return err
		}
	}
	return nil
}

func resolutionChangesMCP(p Resolution) bool {
	for _, target := range p.Targets {
		for _, path := range []string{"mcp.yaml", "plugins.lock"} {
			before, beforeExists := target.Before.Entries[path]
			after, afterExists := target.After.Entries[path]
			if beforeExists != afterExists || before != after {
				return true
			}
		}
	}
	return false
}

func publicationContainer(before workstore.Tree, output native.Target) (workstore.Tree, error) {
	skills := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if entry, ok := before.Entries["skills"]; ok {
		if !entry.Dir {
			return skills, result.Fail("PUBLICATION_PLAN_INVALID", "工具技能槽位被文件占用", 5)
		}
		skills.Exists = true
	}
	for path, e := range before.Entries {
		if strings.HasPrefix(path, "skills/") {
			skills.Entries[strings.TrimPrefix(path, "skills/")] = e
		}
	}
	if workstore.Digest(skills) != workstore.Digest(output.Before) {
		return skills, result.Fail("PLAN_STALE", "工具容器与编译时技能目录观察不一致", 3)
	}
	after := workstore.Clone(before)
	delete(after.Entries, "skills")
	for path := range after.Entries {
		if strings.HasPrefix(path, "skills/") {
			delete(after.Entries, path)
		}
	}
	if output.After.Exists {
		after.Exists = true
		directory, exists := before.Entries["skills"]
		if !exists {
			directory = workstore.Directory()
		}
		after.Entries["skills"] = directory
		for path, e := range output.After.Entries {
			after.Entries["skills/"+path] = e
		}
	}
	return after, nil
}

// Re-derive every writable byte from the immutable compiler results. Editing
// both local checksums cannot turn container preservation into ownership of
// another tool file, or add a source/control mutation absent from decisions.
func (m *Manager) validatePublicationTransaction(p PublicationPlan) error {
	bad := func() error {
		return result.Fail("PUBLICATION_PLAN_INVALID", "正式事务与逐项决策或工具编译结果不一致", 5)
	}
	resolution := p.Resolution
	if resolution.Owner != p.Owner || resolution.Scope != p.Scope || resolution.Kind != "replica-resolution" || resolution.Source.ID != resolution.Request.From || !uuid.MatchString(resolution.Source.ID) || !resolution.Repair && resolution.Version != 2 || resolution.Repair && resolution.Version != 1 {
		return bad()
	}
	selected := map[string]bool{resolution.Request.From: true}
	for _, id := range resolution.Request.Targets {
		if selected[id] || !uuid.MatchString(id) {
			return bad()
		}
		selected[id] = true
	}
	inputs := map[string]publicationInput{}
	for _, input := range p.Inputs {
		if !selected[input.Copy.ID] || len(input.Snapshot) != 64 || len(input.Configuration) != 64 {
			return bad()
		}
		if _, ok := inputs[input.Copy.ID]; ok {
			return bad()
		}
		if err := m.validateBinding(input.Copy); err != nil {
			return err
		}
		inputs[input.Copy.ID] = input
	}
	if len(inputs) != len(selected) || string(data(inputs[resolution.Source.ID].Copy)) != string(data(resolution.Source)) {
		return bad()
	}
	for i, t := range resolution.Targets {
		input := inputs[t.Copy.ID]
		if t.Copy.ID == resolution.Source.ID || string(data(input.Copy)) != string(data(t.Copy)) || input.Snapshot != workstore.Digest(t.Before) || input.Configuration != p.Generation.Targets[i].ConfigurationDigest {
			return bad()
		}
		if resolution.Repair && (workstore.Digest(t.Before) != workstore.Digest(t.After) || len(resolution.Request.Decisions) != 0 || workstore.Digest(resolution.ControlBefore) != workstore.Digest(resolution.ControlAfter)) {
			return bad()
		}
	}
	byPath := map[string]workstore.BatchTarget{}
	for _, target := range p.Transaction.Targets {
		if _, exists := byPath[target.Path]; exists {
			return bad()
		}
		byPath[target.Path] = target
	}
	expect := func(key string, before, after workstore.Tree, file bool) error {
		t, ok := byPath[key]
		if !ok || t.File != file || workstore.Digest(t.Before) != workstore.Digest(before) || workstore.Digest(t.After) != workstore.Digest(after) {
			return bad()
		}
		delete(byPath, key)
		return nil
	}
	if err := expect("control", p.Resolution.ControlBefore, p.Resolution.ControlAfter, false); err != nil {
		return err
	}
	for i, t := range p.Resolution.Targets {
		if err := expect("copies/"+t.Copy.ID+"/.aidock", t.Before, t.After, false); err != nil {
			return err
		}
		for _, o := range p.Generation.Targets[i].Outputs {
			key := generatedPath(t.Copy.ID, o.Adapter.ID)
			target, ok := byPath[key]
			if !ok {
				return bad()
			}
			after, err := publicationContainer(target.Before, o)
			if err != nil {
				return err
			}
			if err = expect(key, target.Before, after, false); err != nil {
				return err
			}
			if o.Rule != nil {
				if err = expect(generatedRulePath(t.Copy.ID, o.Adapter.ID), o.Rule.Before, o.Rule.After, true); err != nil {
					return err
				}
			}
		}
	}
	if len(byPath) != 0 || p.Transaction.Action != "apply" || p.Transaction.Reference != "" {
		return bad()
	}
	return nil
}

func (m *Manager) PlanPublication(ctx context.Context, req PublicationRequest) (PublicationPlan, error) {
	g, resolution, err := m.loadGeneration(req.Generation, req.Digest)
	if err != nil {
		return PublicationPlan{}, err
	}
	r, err := m.GenerationCheckShow(ctx, req.Check)
	if err != nil {
		return PublicationPlan{}, err
	}
	if req.CheckDigest == "" || req.CheckDigest != r.Digest {
		return PublicationPlan{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要检查记录的完整摘要", 4)
	}
	p := PublicationPlan{Version: 1, Kind: "replica-publication", ID: workstore.ID(), Owner: m.Source.Owner, Scope: m.Source.Scope, InitiatorRoot: m.Source.Store.Root, InitiatorCopy: m.Source.Copy, Created: time.Now().UTC().Format(time.RFC3339Nano), Resolution: resolution, Generation: g, Check: r, Inputs: []publicationInput{}, Boundary: "此确认将写入明确列出的已登记目标：逐项选定的 owned 技能源、通过精确版本隔离发现与规则来源检查的工具技能包/规则托管区块，以及共同基线。发布者只读；目标本机覆盖、手写内容、本机排除与 MCP 原生连接保留。整批失败恢复，支持回退和撤销；不启动工具或执行技能，L3/L4 与旧会话未验证。"}
	if err = m.publicationQualification(p); err != nil {
		return p, err
	}
	if err = m.revalidateGeneration(ctx, g, resolution); err != nil {
		return p, err
	}
	registry, err := m.List()
	if err != nil {
		return p, err
	}
	selected := map[string]bool{resolution.Request.From: true}
	for _, id := range resolution.Request.Targets {
		selected[id] = true
	}
	for _, b := range registry.Copies {
		if !selected[b.ID] {
			continue
		}
		s, err := m.copySource(b)
		if err != nil {
			return p, err
		}
		t, err := s.Store.Capture(s.Store.Root)
		if err != nil {
			return p, err
		}
		configuration, err := s.ConfigurationDigest()
		if err != nil {
			return p, err
		}
		p.Inputs = append(p.Inputs, publicationInput{b, workstore.Digest(t), configuration})
	}
	sort.Slice(p.Inputs, func(i, j int) bool { return p.Inputs[i].Copy.ID < p.Inputs[j].Copy.ID })
	targets, err := m.publicationTargets(resolution, g)
	if err != nil {
		return p, err
	}
	c := m.publicationCoordinator(p)
	p.Transaction, err = c.PreviewObserved("apply", targets)
	if err != nil {
		return p, err
	}
	if err = m.validatePublicationTransaction(p); err != nil {
		return p, err
	}
	if err = m.revalidateGeneration(ctx, g, resolution); err != nil {
		return p, err
	}
	if err = m.publicationReadInputs(ctx, p, false); err != nil {
		return p, err
	}
	p.Digest = publicationDigest(p)
	return p, saveLocal(m.publicationFile(p.ID), p)
}

func (m *Manager) publicationTargets(resolution Resolution, g Generation) ([]workstore.BatchTarget, error) {
	targets := []workstore.BatchTarget{{Path: "control", Before: resolution.ControlBefore, After: resolution.ControlAfter}}
	for i, t := range resolution.Targets {
		targets = append(targets, workstore.BatchTarget{Path: "copies/" + t.Copy.ID + "/.aidock", Before: t.Before, After: t.After})
		for _, o := range g.Targets[i].Outputs {
			before, err := m.Control.Capture(filepath.Dir(o.Root))
			if err != nil {
				return nil, err
			}
			after, err := publicationContainer(before, o)
			if err != nil {
				return nil, err
			}
			targets = append(targets, workstore.BatchTarget{Path: generatedPath(t.Copy.ID, o.Adapter.ID), Before: before, After: after})
			if o.Rule != nil {
				targets = append(targets, workstore.BatchTarget{Path: generatedRulePath(t.Copy.ID, o.Adapter.ID), File: true, Before: o.Rule.Before, After: o.Rule.After})
			}
		}
	}
	return targets, nil
}

func (m *Manager) loadPublication(id string) (PublicationPlan, error) {
	p, err := m.readPublication(id)
	if err != nil {
		return p, err
	}
	if p.InitiatorRoot != m.Source.Store.Root || p.InitiatorCopy != m.Source.Copy {
		return p, result.Fail("BACKUP_INVALID", "正式发布必须从原发起目录与绑定打开或恢复", 5)
	}
	return p, nil
}

// Validate every stored record before history filters by its initiating binding.
// A valid record from another copy must not hide this copy's own history.
func (m *Manager) readPublication(id string) (PublicationPlan, error) {
	var p PublicationPlan
	if !uuid.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "需要正式发布 UUID", 2)
	}
	b, err := workstore.ReadFile(m.publicationFile(id))
	if err != nil {
		return p, err
	}
	if err = decode(b, &p); err != nil {
		return p, err
	}
	if p.Version != 1 || p.Kind != "replica-publication" || p.ID != id || p.Owner != m.Source.Owner || p.Scope != m.Source.Scope || p.Digest != publicationDigest(p) || p.Resolution.Digest != resolutionDigest(p.Resolution) || len(p.Resolution.Targets) == 0 || len(p.Inputs) != len(p.Resolution.Request.Targets)+1 {
		return p, result.Fail("BACKUP_INVALID", "正式发布身份、原绑定或恢复描述损坏", 5)
	}
	if err = validateGeneration(p.Generation, p.Resolution); err != nil {
		return p, err
	}
	if err = m.validatePublicationTransaction(p); err != nil {
		return p, err
	}
	c := m.publicationCoordinator(p)
	transaction, err := c.LoadPlan(p.Transaction.ID)
	if err != nil {
		return p, err
	}
	if string(data(transaction)) != string(data(p.Transaction)) {
		return p, result.Fail("BACKUP_INVALID", "发布事务与预览不一致", 5)
	}
	return p, nil
}

// After writes, only read-only sources are checked against original bytes.
// Every written root is checked by Coordinator before this final guard.
func (m *Manager) publicationReadInputs(ctx context.Context, p PublicationPlan, after bool) error {
	written := map[string]bool{}
	for _, t := range p.Resolution.Targets {
		written[t.Copy.ID] = true
	}
	for _, input := range p.Inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if branch(input.Copy.Root) != input.Copy.Branch {
			return result.Fail("PLAN_STALE", "发布期间分支变化", 3)
		}
		s, err := m.copySource(input.Copy)
		if err != nil {
			return err
		}
		if !after || !written[input.Copy.ID] {
			t, err := s.Store.Capture(s.Store.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(t) != input.Snapshot {
				return result.Fail("PLAN_STALE", "发布者或未改写副本在发布期间变化", 3)
			}
		}
		configuration, err := s.ConfigurationDigest()
		if err != nil {
			return err
		}
		if configuration != input.Configuration {
			return result.Fail("PLAN_STALE", "发布期间本机覆盖或全局库变化", 3)
		}
	}
	for i, target := range p.Generation.Targets {
		for _, output := range target.Outputs {
			if output.Rule != nil && output.Rule.Override != nil {
				input := output.Rule.Override
				t, err := m.Control.CaptureFile(filepath.Join(filepath.Dir(p.Resolution.Targets[i].Copy.Root), input.Path))
				if err != nil {
					return err
				}
				if workstore.Digest(t) != workstore.Digest(input.Before) {
					return result.Fail("PLAN_STALE", "发布期间规则优先级输入变化", 3)
				}
			}
		}
	}
	return m.publicationQualification(p)
}

func (m *Manager) withPublicationInputs(p PublicationPlan, fn func() error) error {
	managers := []*source.Manager{}
	readOnly := []*source.Manager{}
	written := map[string]bool{}
	for _, t := range p.Resolution.Targets {
		written[t.Copy.ID] = true
	}
	for _, input := range p.Inputs {
		s, err := m.copySource(input.Copy)
		if err != nil {
			return err
		}
		managers = append(managers, s)
		if !written[input.Copy.ID] {
			readOnly = append(readOnly, s)
		}
	}
	sort.Slice(readOnly, func(i, j int) bool {
		return strings.ToLower(readOnly[i].Store.Root) < strings.ToLower(readOnly[j].Store.Root)
	})
	var acquire func(int) error
	acquire = func(i int) error {
		if i == len(readOnly) {
			return source.WithConfigurationLocks(managers, fn)
		}
		return readOnly[i].Store.Locked(func() error { return acquire(i + 1) })
	}
	return acquire(0)
}

func (m *Manager) ApplyPublication(ctx context.Context, id, digest string) (Publication, error) {
	p, err := m.loadPublication(id)
	if err != nil {
		return Publication{}, err
	}
	if digest == "" || digest != p.Digest {
		return Publication{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要正式发布预览完整摘要", 4)
	}
	// Full reconstruction outside the all-root lock; repeat it under the
	// control/source/configuration locks using the non-reentrant helper.
	if err = m.revalidateGeneration(ctx, p.Generation, p.Resolution); err != nil {
		return Publication{}, err
	}
	err = m.withPublicationInputs(p, func() error {
		c := m.publicationCoordinator(p)
		c.Validate = func(workstore.BatchPlan) error {
			if err := m.revalidateWith(ctx, p.Resolution, true); err != nil {
				return err
			}
			if err := m.checkGenerationCompiler(ctx, p.Generation, p.Resolution); err != nil {
				return err
			}
			return m.publicationReadInputs(ctx, p, false)
		}
		c.BeforeCommit = func(workstore.BatchPlan) error { return m.publicationReadInputs(ctx, p, true) }
		_, err := c.Apply(ctx, p.Transaction.ID, p.Transaction.Digest)
		return err
	})
	if err != nil {
		return Publication{}, err
	}
	return m.publicationReport(p)
}

func (m *Manager) publicationReport(p PublicationPlan) (Publication, error) {
	c := m.publicationCoordinator(p)
	js, err := c.History()
	if err != nil {
		return Publication{}, err
	}
	r := Publication{ID: p.ID, Created: p.Created, Status: "READY", Source: p.Resolution.Source, Roots: c.Roots, Transactions: []Transaction{}, Boundary: p.Boundary}
	for _, target := range p.Resolution.Targets {
		r.Members = append(r.Members, target.Copy)
	}
	for _, j := range js {
		if j.State == "prepared" {
			r.Pending = true
		}
		pending, err := c.MarkersPending(j.Plan.ID)
		if err != nil {
			r.Warning = err.Error()
		} else if pending {
			r.Pending = true
		}
		t := Transaction{ID: j.Plan.ID, Action: j.Plan.Action, Reference: j.Plan.Reference, State: j.State, Updated: j.Updated}
		for _, target := range j.Plan.Targets {
			t.Changes += len(workstore.Changes(target.Before, target.After))
			if target.File {
				t.RuleFiles++
			} else if strings.Contains(target.Path, "/generated/") {
				t.Generated++
			} else if target.Path != "control" {
				t.Targets++
			}
		}
		r.Transactions = append(r.Transactions, t)
	}
	if r.Pending {
		r.Status = "PENDING"
	} else if len(r.Transactions) > 0 {
		t := r.Transactions[0]
		if t.State == "recovered" {
			r.Status = "RECOVERED"
		} else {
			r.Status = map[string]string{"apply": "APPLIED", "rollback": "ROLLED_BACK", "undo-rollback": "UNDONE"}[t.Action]
		}
	}
	return r, nil
}
func (m *Manager) PublicationShow(id string) (Publication, error) {
	p, err := m.loadPublication(id)
	if err != nil {
		return Publication{}, err
	}
	return m.publicationReport(p)
}
func (m *Manager) PublicationHistory() ([]Publication, error) {
	dir := filepath.Join(m.publicationsDir(), "plans")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Publication{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []Publication{}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".resolution-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, result.Fail("BACKUP_INVALID", "未知正式发布记录", 5)
		}
		p, err := m.readPublication(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if p.InitiatorRoot != m.Source.Store.Root || p.InitiatorCopy != m.Source.Copy {
			continue
		}
		r, err := m.publicationReport(p)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created > rows[j].Created })
	return rows, nil
}
func (m *Manager) ReversePublication(id, transaction string) (workstore.BatchPlan, error) {
	p, err := m.loadPublication(id)
	if err != nil {
		return workstore.BatchPlan{}, err
	}
	return m.publicationCoordinator(p).Reverse(transaction)
}
func (m *Manager) ApplyPublicationReverse(ctx context.Context, id, plan, digest string) (Publication, error) {
	owned, err := m.hasPublicationReplay(id, plan)
	if err != nil {
		return Publication{}, err
	}
	if owned {
		return m.ApplyPublicationReplay(ctx, id, plan, digest)
	}
	p, err := m.loadPublication(id)
	if err != nil {
		return Publication{}, err
	}
	c := m.publicationCoordinator(p)
	tx, err := c.LoadPlan(plan)
	if err != nil {
		return Publication{}, err
	}
	if tx.Action != "rollback" && tx.Action != "undo-rollback" {
		return Publication{}, result.Fail("PLAN_KIND_MISMATCH", "此入口仅确认正式同步回退或撤销", 2)
	}
	if _, err = c.Apply(ctx, plan, digest); err != nil {
		return Publication{}, err
	}
	return m.publicationReport(p)
}
func (m *Manager) RecoverPublication(id string) (Publication, error) {
	p, err := m.loadPublication(id)
	if err != nil {
		return Publication{}, err
	}
	if _, err = m.publicationCoordinator(p).Recover(); err != nil {
		return Publication{}, err
	}
	return m.publicationReport(p)
}

func (m *Manager) InspectPublicationRecovery(id string) (workstore.RecoveryInspection, error) {
	p, err := m.loadPublication(id)
	if err != nil {
		return workstore.RecoveryInspection{}, err
	}
	return m.publicationCoordinator(p).InspectRecovery()
}
