package replica

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) publicationCopyIdentity(ctx context.Context, p PublicationPlan, copy Binding, recovery bool) error {
	s, err := source.New(copy.Root, m.Source.Store.Local, p.Owner, p.Scope)
	if err != nil {
		return err
	}
	if err = s.BindBridgeObservation(m.Source.BridgeFile, copy.ID); err != nil {
		return err
	}
	return s.ValidateReplayIdentity(ctx, recovery)
}

// Recovery/inspection reads only identity files, not observation blobs, live
// Profiles or mutable execution approvals. A changed but unmodified batch
// member is not overwritten or adopted as this operation's repair target.
func (m *Manager) publicationRecoveryIdentity(p PublicationPlan, tx workstore.BatchPlan) error {
	changed := map[string]bool{}
	for _, target := range tx.Targets {
		if workstore.Digest(target.Before) == workstore.Digest(target.After) {
			continue
		}
		if target.Path == "control" {
			body, err := workstore.ReadFile(filepath.Join(m.Control.Root, "replicas.json"))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if value, err := config.Parse(body); err == nil {
				if fields, ok := value.(map[string]any); ok {
					if version, exists := fields["schema_version"]; exists && version != 1 {
						return result.Fail("REPLAY_IDENTITY_SCHEMA_UNSUPPORTED", "登记文件声明未知版本，不能在恢复中降级", 4)
					}
					for key, want := range map[string]string{"owner": p.Owner, "scope_id": p.Scope} {
						if got, ok := fields[key].(string); ok && got != "" && got != want {
							return result.Fail("SOURCE_IDENTITY_MISMATCH", "登记文件已声明另一项目或作用域，保留恢复标记", 3)
						}
					}
				}
			}
		}
		parts := strings.Split(target.Path, "/")
		if len(parts) > 2 && parts[0] == "copies" {
			changed[parts[1]] = true
		}
	}
	for _, target := range p.Resolution.Targets {
		if changed[target.Copy.ID] {
			if err := m.publicationCopyIdentity(context.Background(), p, target.Copy, true); err != nil {
				return err
			}
		}
	}
	return nil
}
