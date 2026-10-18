package native

import (
	"context"
	"sort"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Frozen old application input plus the actual common restoration role. This
// object is not decodable from JSON and does not grant permission to write.
type PublicationRestorationCandidate struct {
	manager *Manager
	proof   projectsnapshot.PublicationCommit
	plan    workstore.BatchPlan
	view    appliedRecord
	draft   source.ReplayDraft
	tools   []string
	digest  string
}

func (c PublicationRestorationCandidate) Digest() string   { return c.digest }
func (c PublicationRestorationCandidate) Tools() []string  { return append([]string{}, c.tools...) }
func (c PublicationRestorationCandidate) Snapshot() string { return c.view.Record.ID }

func (m *Manager) PublicationRestoreCandidate(ctx context.Context, proof projectsnapshot.PublicationCommit, plan, digest, binding string) (PublicationRestorationCandidate, error) {
	var out PublicationRestorationCandidate
	if !validDigest.MatchString(binding) {
		return out, result.Fail("INVALID_INPUT", "需要原应用的完整绑定摘要", 2)
	}
	review, err := m.InspectPublicationRestoration(ctx, proof, plan, digest)
	if err != nil {
		return out, err
	}
	found := false
	for _, a := range review.views {
		if a.Record.ActiveKey == binding {
			out.view, found = a, true
			break
		}
	}
	if !found {
		return out, result.Fail("PUBLICATION_RESTORE_VIEW_NOT_SELECTED", "原应用不属于此成员明确恢复的连接，不能独立请求旧合同授权", 4)
	}
	// Full applications own a complete tool receiver. Several old applications
	// can be coordinated independently when they belong to different tools.
	// Mixed view identities inside one receiver are ambiguous and fail closed.
	c, _ := proof.Coordinator(m.Source.Store.Local)
	ledger, err := m.publicationRestorationLedger(c.Blobs, review.member, true)
	if err != nil {
		return out, err
	}
	for tool, receiver := range ledger.Tools {
		selected, mixed := false, false
		for _, field := range receiver.Fields {
			selected = selected || field.AppliedKey == binding
			mixed = mixed || field.AppliedKey != binding
		}
		if selected && mixed {
			return out, result.Fail("PUBLICATION_RESTORE_MIXED_RECEIVER", "同一工具接收器混合多个原应用，须先明确协调完整接收合同", 4)
		}
		if selected {
			out.tools = append(out.tools, tool)
		}
	}
	sort.Strings(out.tools)
	if len(out.tools) == 0 {
		return out, result.Fail("PUBLICATION_RESTORE_VIEW_NOT_SELECTED", "原应用没有要恢复的工具接收器", 4)
	}
	out.draft, err = m.Source.FrozenReplayCandidate(ctx, out.view.Frozen)
	if err != nil {
		return PublicationRestorationCandidate{}, err
	}
	out.manager, out.proof, out.plan = m, review.proof, review.plan
	out.digest = workstore.Hash(jsonData(struct {
		Publication, Plan, PlanSHA, Binding, Snapshot, Commit, Draft string
		Tools                                                        []string
	}{proof.Publication, plan, digest, binding, out.view.Record.ID, out.view.Record.Commit, out.draft.Digest, out.tools}))
	return out, nil
}

func (c PublicationRestorationCandidate) validate(ctx context.Context) error {
	if c.manager == nil {
		return result.Fail("PUBLICATION_RESTORE_INVALID", "缺少原应用恢复候选", 5)
	}
	current, err := c.manager.PublicationRestoreCandidate(ctx, c.proof, c.plan.ID, c.plan.Digest, c.view.Record.ActiveKey)
	if err != nil {
		return err
	}
	if current.digest != c.digest {
		return result.Fail("PLAN_STALE", "共同恢复节点、原应用归档或当前草稿在审查后变化", 3)
	}
	return nil
}

func (c PublicationRestorationCandidate) Permissions(ctx context.Context) (source.ReplayPermissionReview, error) {
	if err := c.validate(ctx); err != nil {
		return source.ReplayPermissionReview{}, err
	}
	return c.manager.Source.ReplayPermissions(ctx, c.draft)
}

func (c PublicationRestorationCandidate) PlanPermission(ctx context.Context, edit pluginpermission.Edit) (PublicationRestorationPermissionPlan, error) {
	if err := c.validate(ctx); err != nil {
		return PublicationRestorationPermissionPlan{}, err
	}
	if edit.Action != "grant" {
		return PublicationRestorationPermissionPlan{}, result.Fail("INVALID_INPUT", "恢复候选只接受明确审查的新授权", 2)
	}
	return c.planPermission(ctx, edit)
}

func (c PublicationRestorationCandidate) ApplyPermission(ctx context.Context, name, id, digest string) (workstore.Journal, error) {
	if err := c.validate(ctx); err != nil {
		return workstore.Journal{}, err
	}
	return c.applyPermission(ctx, name, id, digest)
}

func (c PublicationRestorationCandidate) Runtime(ctx context.Context) (source.ReplayRuntimeReview, error) {
	if err := c.validate(ctx); err != nil {
		return source.ReplayRuntimeReview{}, err
	}
	return c.manager.Source.ReplayRuntime(ctx, c.draft)
}

func (c PublicationRestorationCandidate) CheckRuntime(ctx context.Context, req source.MCPRuntimeRequest) (runtimecheck.Event, error) {
	if err := c.validate(ctx); err != nil {
		return runtimecheck.Event{}, err
	}
	return c.manager.Source.CheckReplayRuntime(ctx, c.draft, req)
}

func (c PublicationRestorationCandidate) preparationInput(ctx context.Context) configurationPreparationInput {
	m := c.manager
	return configurationPreparationInput{candidate: c.digest, kind: "publication-restore-candidate",
		compile: func(req CheckRequest) (Plan, *Manager, error) {
			if err := c.validate(ctx); err != nil {
				return Plan{}, nil, err
			}
			tools := append([]string{}, req.Tools...)
			sort.Strings(tools)
			if string(jsonData(tools)) != string(jsonData(c.tools)) {
				return Plan{}, nil, result.Fail("PUBLICATION_RESTORE_TOOL_SELECTION_MISMATCH", "恢复检查必须覆盖原应用要恢复的全部工具接收器", 4)
			}
			return m.compileFutureConfiguration(ctx, req, "publication-restore-candidate", func(use func(source.Export) error) error {
				return m.Source.WithReplayExport(ctx, c.draft, use)
			})
		},
		connect: func(tools []string, executable, home string) ([]workstore.BatchTarget, []source.MCPSessionRequest, error) {
			connections, sessions := []workstore.BatchTarget{}, []source.MCPSessionRequest{}
			err := m.Source.WithReplayMCPClientConfigs(ctx, c.draft, executable, home, func(configs []source.MCPClientConfiguration, wanted []source.MCPSessionRequest) error {
				requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
				for i, session := range wanted {
					requests = append(requests, MCPConnectionRequest{Action: "install", Tools: tools, Session: session})
					pointers = append(pointers, &configs[i])
				}
				var err error
				connections, err = m.compileConnectionSet(tools, requests, pointers, executable, home, true)
				if err == nil {
					sessions = append(sessions, wanted...)
				}
				return err
			})
			return connections, sessions, err
		},
	}
}

// Checking uses today's actual native observations and the original frozen
// contract. Desired restoration images are consumed later by the joint owner;
// this receipt cannot be passed to ordinary configuration publication.
func (c PublicationRestorationCandidate) Prepare(ctx context.Context, req CheckRequest, executable, home string) (ConfigurationPreparation, error) {
	if err := c.validate(ctx); err != nil {
		return ConfigurationPreparation{}, err
	}
	if len(req.Tools) == 0 {
		req.Tools = c.Tools()
	}
	return c.manager.prepareConfiguration(ctx, c.preparationInput(ctx), req, executable, home)
}

func (c PublicationRestorationCandidate) ReadPreparation(ctx context.Context, id string) (ConfigurationPreparation, error) {
	if c.manager == nil {
		return ConfigurationPreparation{}, result.Fail("PUBLICATION_RESTORE_INVALID", "缺少原应用恢复候选", 5)
	}
	p, err := c.manager.loadConfigurationPreparationKind(id, "publication-restore-candidate")
	if err != nil {
		return ConfigurationPreparation{}, err
	}
	out := p.Report
	err = c.validate(ctx)
	if err == nil {
		err = c.manager.revalidatePreparedConfiguration(ctx, c.preparationInput(ctx), p)
	}
	if err != nil {
		code, known := preparationErrorCode(err)
		if !known {
			code = "PUBLICATION_RESTORE_PREPARATION_STALE"
		}
		out.Status, out.Fresh, out.MCPReady = "STALE", false, false
		out.Changes = []ReplayNativeChange{}
		out.Check.Stale, out.Check.StaleReason = true, code
		out.Gates = append(append([]inventory.Finding{}, out.Gates...), inventory.Finding{Status: "UNKNOWN", Code: code})
	}
	return out, nil
}
