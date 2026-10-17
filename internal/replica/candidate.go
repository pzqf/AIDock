package replica

import (
	"context"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Only a saved, revalidated resolution can supply future source material.
// Consumers cannot post arbitrary trees, local paths, bindings or old grants.
type CandidateRequest struct {
	Plan   string `json:"plan_id"`
	Digest string `json:"digest"`
	Copy   string `json:"copy_id"`
}

type CandidateReview struct {
	Request      CandidateRequest              `json:"request"`
	Copy         Binding                       `json:"copy"`
	Candidate    string                        `json:"candidate_digest"`
	Artifacts    source.MCPArtifactState       `json:"artifacts"`
	Installation plugininstall.State           `json:"installation"`
	Permission   source.ReplayPermissionReview `json:"permission"`
	Runtime      source.ReplayRuntimeReview    `json:"runtime"`
	Boundary     string                        `json:"boundary"`
}

type CandidateArtifactRequest struct {
	CandidateRequest
	Edit pluginartifact.Edit `json:"edit"`
}
type CandidateInstallRequest struct {
	CandidateRequest
	Edit plugininstall.Edit `json:"edit"`
}
type CandidatePermissionRequest struct {
	CandidateRequest
	Edit pluginpermission.Edit `json:"edit"`
}
type CandidateRuntimeRequest struct {
	CandidateRequest
	Check source.MCPRuntimeRequest `json:"check"`
}
type CandidateConfirmation struct {
	CandidateRequest
	Name            string `json:"name"`
	OperationPlan   string `json:"operation_plan_id"`
	OperationDigest string `json:"operation_digest"`
}

func (m *Manager) withCandidate(ctx context.Context, req CandidateRequest, use func(*source.Manager, source.ConfigurationCandidate, Binding) error) error {
	if !uuid.MatchString(req.Copy) || use == nil {
		return result.Fail("INVALID_INPUT", "需要候选计划、完整摘要与明确目标副本", 2)
	}
	return m.Control.Locked(func() error {
		p, err := m.loadResolution(req.Plan, req.Digest)
		if err != nil {
			return err
		}
		if p.Version != 2 || p.Repair {
			return result.Fail("REPLICA_CONFIGURATION_CANDIDATE_REQUIRED", "此入口需要包含共享配置观察的副本候选", 4)
		}
		if err = m.revalidateWith(ctx, p, true); err != nil {
			return err
		}
		for _, t := range p.Targets {
			if t.Copy.ID != req.Copy {
				continue
			}
			s, err := m.copySource(t.Copy)
			if err != nil {
				return err
			}
			if err = transfer(m.Control, s.Store, t.Before, t.After); err != nil {
				return err
			}
			c, err := s.DraftConfigurationCandidate(ctx, t.Before, t.After, t.ConfigurationDigest)
			if err != nil {
				return err
			}
			return use(s, c, t.Copy)
		}
		return result.Fail("REPLICA_TARGET_NOT_SELECTED", "目标不在此候选的明确选择中；未准备、授权或执行", 4)
	})
}

func (m *Manager) Candidate(ctx context.Context, req CandidateRequest) (CandidateReview, error) {
	out := CandidateReview{Request: req, Boundary: "按目标未来配置分别检查完整包、安装、授权和运行时；读取不启动进程，不写规范源或工具。各项准备需独立预览确认，不复制发布者的凭据、授权或运行证据，也不代表已经正式同步。"}
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, b Binding) (err error) {
		out.Copy, out.Candidate = b, c.Digest()
		if out.Artifacts, err = s.CandidateArtifacts(ctx, c); err != nil {
			return err
		}
		if out.Installation, err = s.CandidateInstallations(ctx, c); err != nil {
			return err
		}
		if out.Permission, err = s.CandidatePermissions(ctx, c); err != nil {
			return err
		}
		out.Runtime, err = s.CandidateRuntime(ctx, c)
		return err
	})
	return out, err
}

func (m *Manager) CandidateArtifactPlan(ctx context.Context, req CandidateRequest, edit pluginartifact.Edit) (pluginartifact.Preview, error) {
	var out pluginartifact.Preview
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.PlanCandidateArtifact(ctx, c, edit)
		return err
	})
	return out, err
}
func (m *Manager) CandidateArtifactApply(ctx context.Context, req CandidateRequest, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.ApplyCandidateArtifact(ctx, c, name, id, digest)
		return err
	})
	return out, err
}
func (m *Manager) CandidateInstallPlan(ctx context.Context, req CandidateRequest, edit plugininstall.Edit) (plugininstall.Preview, error) {
	var out plugininstall.Preview
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.PlanCandidateInstallation(ctx, c, edit)
		return err
	})
	return out, err
}
func (m *Manager) CandidateInstallApply(ctx context.Context, req CandidateRequest, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.ApplyCandidateInstallation(ctx, c, name, id, digest)
		return err
	})
	return out, err
}
func (m *Manager) CandidatePermissionPlan(ctx context.Context, req CandidateRequest, edit pluginpermission.Edit) (pluginpermission.Preview, error) {
	var out pluginpermission.Preview
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.PlanCandidatePermission(ctx, c, edit)
		return err
	})
	return out, err
}
func (m *Manager) CandidatePermissionApply(ctx context.Context, req CandidateRequest, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.ApplyCandidatePermission(ctx, c, name, id, digest)
		return err
	})
	return out, err
}
func (m *Manager) CandidateRuntimeCheck(ctx context.Context, req CandidateRequest, check source.MCPRuntimeRequest) (runtimecheck.Event, error) {
	var out runtimecheck.Event
	err := m.withCandidate(ctx, req, func(s *source.Manager, c source.ConfigurationCandidate, _ Binding) (err error) {
		out, err = s.CheckCandidateRuntime(ctx, c, check)
		return err
	})
	return out, err
}
