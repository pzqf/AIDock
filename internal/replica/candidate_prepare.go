package replica

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

type CandidatePreparationRequest struct {
	CandidateRequest
	Preparation native.CheckRequest `json:"preparation"`
}
type CandidatePreparedRequest struct {
	CandidateRequest
	ID string `json:"preparation_id"`
}

func ParseCandidatePreparationRequest(raw []byte) (CandidatePreparationRequest, error) {
	var out CandidatePreparationRequest
	bad := func() (CandidatePreparationRequest, error) {
		return CandidatePreparationRequest{}, result.Fail("INVALID_INPUT", "需要原候选身份与 preparation；拒绝重复键、空值、别名、未知字段、范围缩减或自定义命令", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	value, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 4 {
		return bad()
	}
	base := map[string]any{}
	for _, key := range []string{"plan_id", "digest", "copy_id"} {
		v, ok := fields[key].(string)
		if !ok {
			return bad()
		}
		base[key] = v
	}
	if _, ok := fields["preparation"].(map[string]any); !ok {
		return bad()
	}
	if err := ParseCandidateInput(data(base), &out.CandidateRequest); err != nil {
		return bad()
	}
	out.Preparation, err = native.ParseProfilePreparationRequest(data(fields["preparation"]))
	if err != nil || out.Preparation.RuleProbe && !out.Preparation.Probe {
		return bad()
	}
	scope, err := native.NormalizeApplicationScope(out.Preparation.ApplicationScope)
	if err != nil || scope != "" {
		return bad()
	}
	out.Preparation.ApplicationScope = ""
	return out, nil
}

func (m *Manager) PrepareCandidate(ctx context.Context, req CandidatePreparationRequest, executable, home string) (native.ConfigurationPreparation, error) {
	var out native.ConfigurationPreparation
	err := m.withCandidate(ctx, req.CandidateRequest, func(s *source.Manager, c source.ConfigurationCandidate, b Binding) (err error) {
		n, err := native.New(s, filepath.Dir(b.Root))
		if err != nil {
			return err
		}
		out, err = n.PrepareConfigurationCandidate(ctx, c, req.Preparation, executable, home)
		return err
	})
	return out, err
}
func (m *Manager) ReadCandidatePreparation(ctx context.Context, req CandidatePreparedRequest) (native.ConfigurationPreparation, error) {
	var out native.ConfigurationPreparation
	err := m.withCandidate(ctx, req.CandidateRequest, func(s *source.Manager, c source.ConfigurationCandidate, b Binding) (err error) {
		n, err := native.New(s, filepath.Dir(b.Root))
		if err != nil {
			return err
		}
		out, err = n.ReadConfigurationPreparation(ctx, c, req.ID)
		return err
	})
	return out, err
}
