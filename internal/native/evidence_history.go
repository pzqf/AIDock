package native

import (
	"context"

	"github.com/pzqf/AIDock/internal/result"
)

// ConfigurationHistory is an identity-checked historical observation. It is
// never current qualification and never contains compiler input or sessions.
type ConfigurationHistory struct {
	Preparation string      `json:"preparation_id"`
	Digest      string      `json:"preparation_digest"`
	Candidate   string      `json:"candidate_digest"`
	Check       CheckReport `json:"check"`
	Boundary    string      `json:"boundary"`
}

// ConfigurationCheckHistory preserves the original candidate report without
// re-running it against today's source, native files, grants or executable.
// Its reference must come from a separately validated publication descriptor.
func (m *Manager) ConfigurationCheckHistory(ctx context.Context, id, digest string) (ConfigurationHistory, error) {
	if err := ctx.Err(); err != nil {
		return ConfigurationHistory{}, err
	}
	if !validDigest.MatchString(digest) {
		return ConfigurationHistory{}, result.Fail("INVALID_INPUT", "历史候选需要原检查的完整摘要", 2)
	}
	p, err := m.loadConfigurationPreparation(id)
	if err != nil {
		return ConfigurationHistory{}, err
	}
	if p.Digest != digest {
		return ConfigurationHistory{}, result.Fail("CHECK_CORRUPT", "历史检查与原完整批次的摘要不一致", 5)
	}
	r := p.Report.Check
	if r.Root != m.Root || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Copy != m.Source.Copy || r.SourceSnapshot != p.Report.Candidate || r.Digest != checkDigest(r) {
		return ConfigurationHistory{}, result.Fail("CHECK_CORRUPT", "历史候选的工具检查身份或摘要不符", 5)
	}
	r.Stale, r.StaleReason = true, "原完整批次的候选检查，仅作历史证据；当前文件、会话和能力须分别核对"
	return ConfigurationHistory{Preparation: id, Digest: digest, Candidate: p.Report.Candidate, Check: r, Boundary: r.StaleReason + "。不重新启动程序，不恢复授权，不提供应用资格。"}, nil
}
