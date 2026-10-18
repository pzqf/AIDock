package replica

import (
	"context"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// A shared strict contract for the forthcoming CLI/API workflow. It accepts
// identities and explicit operations only; source trees, roots, executables,
// native text, remembered grants and arbitrary commands cannot be supplied.
type FullPublicationOperation struct {
	Publication   string
	Plan          string
	Digest        string
	Transaction   string
	Copies        []string
	Copy          string
	Binding       string
	Candidate     string
	Name          string
	Operation     string
	OperationSHA  string
	Preparation   string
	Authorization pluginpermission.Edit
	Runtime       source.MCPRuntimeRequest
	Check         native.CheckRequest
	Checks        []FullPublicationRestorationChecks
	Artifact      pluginartifact.Edit
	Installation  plugininstall.Edit
}

func FullPublicationOperationNames() []string {
	return []string{"full-apply", "full-execution", "full-inspect-recovery", "full-recover", "full-restore-plan", "full-restore-show", "full-restore-confirm", "full-restore-review", "full-restore-artifact-plan", "full-restore-artifact-confirm", "full-restore-install-plan", "full-restore-install-confirm", "full-restore-permission-plan", "full-restore-permission-confirm", "full-restore-runtime-check", "full-restore-prepare", "full-restore-prepared", "full-restore-regenerate"}
}

func IsFullPublicationOperation(action string) bool {
	for _, name := range FullPublicationOperationNames() {
		if action == name {
			return true
		}
	}
	return false
}

func ParseFullPublicationOperation(action string, raw []byte) (FullPublicationOperation, error) {
	var out FullPublicationOperation
	bad := func() (FullPublicationOperation, error) {
		return FullPublicationOperation{}, result.Fail("INVALID_INPUT", "需要明确完整批次、恢复提案和本次独立操作；拒绝重复键、空值、别名、未知字段、范围缩减或自定义材料", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	fields, ok := v.(map[string]any)
	if err != nil || !ok {
		return bad()
	}
	base := []string{"publication_id"}
	switch action {
	case "full-apply":
		base = append(base, "digest")
	case "full-execution", "full-inspect-recovery", "full-recover":
	case "full-restore-plan":
		base = append(base, "transaction", "copies")
	case "full-restore-show", "full-restore-confirm":
		base = append(base, "plan_id", "digest")
	case "full-restore-regenerate":
		base = append(base, "plan_id", "digest", "checks")
	case "full-restore-review", "full-restore-artifact-plan", "full-restore-artifact-confirm", "full-restore-install-plan", "full-restore-install-confirm", "full-restore-permission-plan", "full-restore-permission-confirm", "full-restore-runtime-check", "full-restore-prepare", "full-restore-prepared":
		base = append(base, "plan_id", "digest", "copy_id", "applied_binding")
		if action != "full-restore-review" {
			base = append(base, "candidate_digest")
		}
		switch action {
		case "full-restore-artifact-plan", "full-restore-install-plan":
			base = append(base, "edit")
		case "full-restore-permission-plan":
			base = append(base, "authorization")
		case "full-restore-permission-confirm", "full-restore-artifact-confirm", "full-restore-install-confirm":
			base = append(base, "name", "operation_plan_id", "operation_digest")
		case "full-restore-runtime-check":
			base = append(base, "runtime")
		case "full-restore-prepare":
			base = append(base, "preparation")
		case "full-restore-prepared":
			base = append(base, "preparation_id")
		}
	default:
		return bad()
	}
	if len(fields) != len(base) {
		return bad()
	}
	for _, key := range base {
		value, exists := fields[key]
		if !exists || value == nil {
			return bad()
		}
		scalar := func(target *string, digest bool) bool {
			text, ok := value.(string)
			if !ok || digest && !fullPublicationSHA.MatchString(text) || !digest && !uuid.MatchString(text) {
				return false
			}
			*target = text
			return true
		}
		switch key {
		case "publication_id":
			if !scalar(&out.Publication, false) {
				return bad()
			}
		case "plan_id":
			if !scalar(&out.Plan, false) {
				return bad()
			}
		case "digest":
			if !scalar(&out.Digest, true) {
				return bad()
			}
		case "transaction":
			if !scalar(&out.Transaction, false) {
				return bad()
			}
		case "copy_id":
			if !scalar(&out.Copy, false) {
				return bad()
			}
		case "applied_binding":
			if !scalar(&out.Binding, true) {
				return bad()
			}
		case "candidate_digest":
			if !scalar(&out.Candidate, true) {
				return bad()
			}
		case "operation_plan_id":
			if !scalar(&out.Operation, false) {
				return bad()
			}
		case "operation_digest":
			if !scalar(&out.OperationSHA, true) {
				return bad()
			}
		case "preparation_id":
			if !scalar(&out.Preparation, false) {
				return bad()
			}
		case "name":
			out.Name, ok = value.(string)
			if !ok || out.Name == "" || len(out.Name) > 120 || workstore.SafeRelative(out.Name) != nil || strings.ContainsAny(out.Name, "/\\") {
				return bad()
			}
		case "copies":
			items, ok := value.([]any)
			if !ok || len(items) == 0 || len(items) > 31 {
				return bad()
			}
			seen := map[string]bool{}
			for _, item := range items {
				id, ok := item.(string)
				if !ok || !uuid.MatchString(id) || seen[id] {
					return bad()
				}
				seen[id] = true
				out.Copies = append(out.Copies, id)
			}
		case "checks":
			items, ok := value.([]any)
			if !ok || len(items) == 0 || len(items) > 31 {
				return bad()
			}
			seen := map[string]bool{}
			for _, item := range items {
				member, ok := item.(map[string]any)
				if !ok || len(member) != 2 {
					return bad()
				}
				copy, ok := member["copy_id"].(string)
				if !ok || !uuid.MatchString(copy) || seen[copy] {
					return bad()
				}
				seen[copy] = true
				checks, ok := member["checks"].([]any)
				if !ok || len(checks) == 0 || len(checks) > 2 {
					return bad()
				}
				ref := FullPublicationRestorationChecks{Copy: copy, Checks: []native.PublicationRestorationCheck{}}
				bindings := map[string]bool{}
				for _, check := range checks {
					fields, ok := check.(map[string]any)
					if !ok || len(fields) != 3 {
						return bad()
					}
					for _, key := range []string{"applied_binding", "check_id", "check_digest"} {
						if _, ok := fields[key].(string); !ok {
							return bad()
						}
					}
					var c native.PublicationRestorationCheck
					if decode(data(fields), &c) != nil || !fullPublicationSHA.MatchString(c.Binding) || !uuid.MatchString(c.ID) || !fullPublicationSHA.MatchString(c.Digest) || bindings[c.Binding] {
						return bad()
					}
					bindings[c.Binding] = true
					ref.Checks = append(ref.Checks, c)
				}
				out.Checks = append(out.Checks, ref)
			}
		case "authorization":
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
			out.Authorization, err = pluginpermission.ParseEdit(data(value))
			if err != nil || out.Authorization.Action != "grant" {
				return bad()
			}
		case "edit":
			e, ok := value.(map[string]any)
			if !ok {
				return bad()
			}
			allowed := map[string]bool{"action": true, "name": true, "expected_sha256": true}
			if action == "full-restore-artifact-plan" {
				allowed["expected_lock_sha256"], allowed["archive_path"] = true, true
				if len(e) != 5 || decode(data(e), &out.Artifact) != nil || out.Artifact.Action != "import" || out.Artifact.Name == "" || out.Artifact.ArchivePath == "" {
					return bad()
				}
			} else {
				allowed["allow_unverified"], allowed["risk_reason"] = true, true
				if len(e) < 3 || len(e) > 5 || decode(data(e), &out.Installation) != nil || out.Installation.Action != "install" || out.Installation.Name == "" {
					return bad()
				}
			}
			for key, v := range e {
				if !allowed[key] {
					return bad()
				}
				if key == "allow_unverified" {
					if _, ok := v.(bool); !ok {
						return bad()
					}
				} else if _, ok := v.(string); !ok {
					return bad()
				}
			}
			for _, key := range []string{"action", "name", "expected_sha256"} {
				if _, ok := e[key].(string); !ok {
					return bad()
				}
			}
		case "runtime":
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
			out.Runtime, err = source.ParseMCPRuntimeRequest(data(value))
			if err != nil || !out.Runtime.Probe || out.Runtime.Name == "" || !fullPublicationSHA.MatchString(out.Runtime.ExpectedSHA256) || !fullPublicationSHA.MatchString(out.Runtime.ExpectedReviewSHA256) {
				return bad()
			}
		case "preparation":
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
			out.Check, err = native.ParseProfilePreparationRequest(data(value))
			scope, scopeErr := native.NormalizeApplicationScope(out.Check.ApplicationScope)
			if err != nil || scopeErr != nil || scope != "" || out.Check.RuleProbe && !out.Check.Probe {
				return bad()
			}
			out.Check.ApplicationScope = ""
		}
	}
	return out, nil
}

type FullPublicationRestorationReview struct {
	Publication  string                        `json:"publication_id"`
	Plan         string                        `json:"plan_id"`
	Copy         string                        `json:"copy_id"`
	Binding      string                        `json:"applied_binding"`
	Candidate    string                        `json:"candidate_digest"`
	Tools        []string                      `json:"tools"`
	Permission   source.ReplayPermissionReview `json:"permission"`
	Artifacts    source.MCPArtifactState       `json:"artifacts"`
	Installation plugininstall.State           `json:"installation"`
	Runtime      source.ReplayRuntimeReview    `json:"runtime"`
	Boundary     string                        `json:"boundary"`
}

func (m *Manager) RunFullPublicationOperation(ctx context.Context, action string, req FullPublicationOperation, executable, home string) (any, error) {
	if !IsFullPublicationOperation(action) {
		return nil, result.Fail("INVALID_INPUT", "未知完整批次操作", 2)
	}
	switch action {
	case "full-apply":
		return m.ApplyFullPublication(ctx, req.Publication, req.Digest)
	case "full-execution":
		p, err := m.loadFullPublication(req.Publication, "")
		if err != nil {
			return nil, err
		}
		return m.fullPublicationExecution(p)
	case "full-inspect-recovery":
		return m.InspectFullPublicationRecovery(req.Publication)
	case "full-recover":
		return m.RecoverFullPublication(req.Publication)
	case "full-restore-plan":
		return m.PlanFullPublicationRestoration(ctx, req.Publication, req.Transaction, req.Copies)
	case "full-restore-show":
		return m.ReadFullPublicationRestoration(ctx, req.Publication, req.Plan, req.Digest)
	case "full-restore-confirm":
		return m.ApplyFullPublicationRestoration(ctx, req.Publication, req.Plan, req.Digest)
	case "full-restore-regenerate":
		return m.PlanRegeneratedFullPublicationRestoration(ctx, req.Publication, req.Plan, req.Digest, req.Checks, executable, home)
	}
	candidate, err := m.FullPublicationRestoreCandidate(ctx, req.Publication, req.Plan, req.Digest, req.Copy, req.Binding)
	if err != nil {
		return nil, err
	}
	if action == "full-restore-review" {
		out := FullPublicationRestorationReview{Publication: req.Publication, Plan: req.Plan, Copy: req.Copy, Binding: req.Binding, Candidate: candidate.Digest(), Tools: candidate.Tools(), Boundary: "按原共同恢复节点审查原应用的精确合同。授权、运行时核验和完整检查均独立明确；不恢复旧权限，不修改源、本机覆盖或工具。新参数仍须进入共同提案后另行确认。"}
		out.Artifacts, err = candidate.Artifacts(ctx)
		if err != nil {
			return nil, err
		}
		out.Installation, err = candidate.Installations(ctx)
		if err != nil {
			return nil, err
		}
		out.Permission, err = candidate.Permissions(ctx)
		if err != nil {
			return nil, err
		}
		out.Runtime, err = candidate.Runtime(ctx)
		if err != nil {
			return nil, err
		}
		fresh, err := candidate.Permissions(ctx)
		if err != nil {
			return nil, err
		}
		if out.Permission.SHA256 != fresh.SHA256 || out.Permission.ReviewSHA256 != fresh.ReviewSHA256 || out.Runtime.PermissionSHA256 != fresh.SHA256 || out.Runtime.ReviewSHA256 != fresh.ReviewSHA256 {
			return nil, result.Fail("PLAN_STALE", "审查期间权限变化，请重新读取原应用合同", 3)
		}
		return out, nil
	}
	if candidate.Digest() != req.Candidate {
		return nil, result.Fail("PLAN_STALE", "恢复原应用或当前草稿在审查后变化", 3)
	}
	switch action {
	case "full-restore-artifact-plan":
		return candidate.PlanArtifact(ctx, req.Artifact)
	case "full-restore-artifact-confirm":
		return candidate.ApplyArtifact(ctx, req.Name, req.Operation, req.OperationSHA)
	case "full-restore-install-plan":
		return candidate.PlanInstallation(ctx, req.Installation)
	case "full-restore-install-confirm":
		return candidate.ApplyInstallation(ctx, req.Name, req.Operation, req.OperationSHA)
	case "full-restore-permission-plan":
		return candidate.PlanPermission(ctx, req.Authorization)
	case "full-restore-permission-confirm":
		return candidate.ApplyPermission(ctx, req.Name, req.Operation, req.OperationSHA)
	case "full-restore-runtime-check":
		return candidate.CheckRuntime(ctx, req.Runtime)
	case "full-restore-prepare":
		return candidate.Prepare(ctx, req.Check, executable, home)
	case "full-restore-prepared":
		return candidate.ReadPreparation(ctx, req.Preparation)
	}
	return nil, result.Fail("INVALID_INPUT", "未知完整批次操作", 2)
}
