package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) prepareLocalProfileStore() (*workstore.Store, error) {
	s, err := m.localProfileStore()
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(s.Root)
	if err = workstore.CheckLinks(parent); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	return s, nil
}

func (m *Manager) localProfilePlan(ctx context.Context, req ProfileEdit) (Preview, error) {
	out := Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}
	if req.ExpectedSHA256 == "" || req.NativeRule != nil || req.RuleFiles != nil || req.NamedProfiles != nil || req.ExpectedRulesSHA256 != "" || req.ExpectedNamed != "" || req.Snapshot != "" && req.LibraryRoot != nil {
		return out, result.Fail("INVALID_INPUT", "本机编辑须提供观察摘要，不能混入共享规则文件或导入动作", 2)
	}
	actions := 0
	if req.Profile != nil {
		actions++
	}
	if req.Clear {
		actions++
	}
	if req.Snapshot != "" {
		actions++
	}
	if req.LibraryRoot != nil && actions == 0 {
		actions++
	}
	if actions != 1 {
		return out, result.Fail("INVALID_INPUT", "本机编辑、清除、回退只能选择一个动作", 2)
	}
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		s, err := m.prepareLocalProfileStore()
		if err != nil {
			return err
		}
		return s.Locked(func() error {
			if err := s.Pending(); err != nil {
				return err
			}
			before, err := s.Capture(s.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(before) != req.ExpectedSHA256 {
				return result.Fail("PLAN_STALE", "本机覆盖已变化；当前草稿保留", 3)
			}
			if _, err = m.readLocalProfile(s, before); err != nil {
				return err
			}
			after := workstore.Tree{Exists: false, Entries: map[string]workstore.Entry{}}
			action := "profile-local-clear"
			if req.Snapshot != "" {
				js, err := s.Journals()
				if err != nil {
					return err
				}
				if len(js) == 0 || js[0].State != "committed" || js[0].Plan.ID != req.Snapshot {
					return result.Fail("SNAPSHOT_NOT_FOUND", "本机回退只接受最近成功事务；请刷新记录", 3)
				}
				if workstore.Digest(before) != workstore.Digest(js[0].Plan.After) {
					return result.Fail("PLAN_STALE", "本机覆盖含事务外修改；保留原文，不能直接回退", 3)
				}
				after = workstore.Clone(js[0].Plan.Before)
				action = "rollback:" + req.Snapshot
			} else {
				library := ""
				if m.Library != nil {
					library = m.Library.Store.Root
				}
				if req.LibraryRoot != nil {
					library = *req.LibraryRoot
				}
				if err = s.PutFile(&after, "binding.json", m.localProfileBinding(library), 0600); err != nil {
					return err
				}
				if !req.Clear {
					local := req.Profile
					if local == nil {
						local, err = m.readLocalProfile(s, before)
						if err != nil {
							return err
						}
					}
					if local != nil {
						v, err := config.Validate("profile", jsonBytes(local))
						if err != nil {
							return result.Fail("PROFILE_INVALID", err.Error(), 5)
						}
						if err = s.PutFile(&after, "profile.yaml", jsonBytes(v), 0600); err != nil {
							return err
						}
					}
				}
				if !req.Clear {
					action = "profile-local-edit"
				}
			}
			local, err := m.readLocalProfile(s, after)
			if err != nil {
				return err
			}
			t, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			if req.ExpectedMCP != "" && req.ExpectedMCP != mcpDigest(t) {
				return result.Fail("PLAN_STALE", "MCP 定义在编辑后变化；本机草稿保留，请重新核对", 3)
			}
			idx, v, err := m.load(&t)
			if err != nil {
				return err
			}
			if _, ok := t.Entries[m.identityFile()]; !ok {
				return result.Fail("SOURCE_NOT_INITIALIZED", "先初始化项目", 4)
			}
			layers, err := m.profileLayers()
			if err != nil {
				return err
			}
			if req.ExpectedLayers != "" && req.ExpectedLayers != layers.Digest {
				return result.Fail("PLAN_STALE", "全局库或本机配置已变化，请重新核对", 3)
			}
			inputs := map[string]string{"source": workstore.Digest(t), "layers": layers.Digest}
			candidate, err := m.localProfileCandidate(s, after)
			if err != nil {
				return err
			}
			candidateLayers, err := candidate.profileLayers()
			if err != nil {
				return err
			}
			inputs["candidate_layers"] = candidateLayers.Digest
			candidateLayers.Local = local
			report, err := candidate.preflight(t, idx, v, candidateLayers)
			if err != nil {
				return err
			}
			if err = blockEnabled(report); err != nil {
				return err
			}
			report.Candidate = true
			out.Preflight, out.ProfileReport = &report, report.ProfileReport
			out.Plan, err = s.PreviewBound(action, before, after, inputs)
			return err
		})
	})
	return out, err
}

// ApplyProfile keeps local confirmation separate from shared-source commands.
// A local plan can never be applied through skill apply or a replica writer.
func (m *Manager) ApplyProfile(ctx context.Context, target, id, digest string) (workstore.Journal, error) {
	if target == "shared" || target == "" {
		return m.Apply(ctx, id, digest)
	}
	if target != "local" || digest == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要明确目标和确认摘要", 2)
	}
	var out workstore.Journal
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		s, err := m.prepareLocalProfileStore()
		if err != nil {
			return err
		}
		s.Validate = func(p workstore.Plan) error {
			if p.Digest != digest || !(strings.HasPrefix(p.Action, "profile-local-") || strings.HasPrefix(p.Action, "rollback:")) {
				return result.Fail("PLAN_STALE", "本机计划摘要或动作不匹配", 3)
			}
			if _, err := m.readLocalProfile(s, p.Before); err != nil {
				return err
			}
			local, err := m.readLocalProfile(s, p.After)
			if err != nil {
				return err
			}
			t, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			layers, err := m.profileLayers()
			if err != nil {
				return err
			}
			if len(p.Inputs) != 3 || p.Inputs["source"] != workstore.Digest(t) || p.Inputs["layers"] != layers.Digest {
				return result.Fail("PLAN_STALE", "共享源、全局库或本机覆盖已在预览后变化", 3)
			}
			idx, v, err := m.load(&t)
			if err != nil {
				return err
			}
			candidate, err := m.localProfileCandidate(s, p.After)
			if err != nil {
				return err
			}
			candidateLayers, err := candidate.profileLayers()
			if err != nil {
				return err
			}
			if p.Inputs["candidate_layers"] != candidateLayers.Digest {
				return result.Fail("PLAN_STALE", "待关联全局库在预览后变化", 3)
			}
			candidateLayers.Local = local
			report, err := candidate.preflight(t, idx, v, candidateLayers)
			if err != nil {
				return err
			}
			return blockEnabled(report)
		}
		out, err = s.Apply(ctx, id)
		if err == nil {
			candidate, e := m.localProfileCandidate(s, out.Plan.After)
			if e != nil {
				return e
			}
			m.Library, m.ProfileLibrary = candidate.Library, candidate.ProfileLibrary
		}
		return err
	})
	return out, err
}

func (m *Manager) localProfileCandidate(s *workstore.Store, t workstore.Tree) (*Manager, error) {
	candidate := *m
	candidate.Library = nil
	candidate.ProfileLibrary = false
	if len(t.Entries) > 0 {
		binding, err := m.readProfileBinding(s, t)
		if err != nil {
			return nil, err
		}
		if binding.LibraryRoot != "" {
			candidate.ProfileLibrary = true
			if err = candidate.AttachLibrary(binding.LibraryRoot); err != nil {
				return nil, err
			}
		}
	}
	return &candidate, nil
}

func (m *Manager) RecoverLocalProfile() (int, error) {
	s, err := m.prepareLocalProfileStore()
	if err != nil {
		return 0, err
	}
	var count int
	err = m.Store.Locked(func() error { var err error; count, err = s.Recover(); return err })
	return count, err
}
