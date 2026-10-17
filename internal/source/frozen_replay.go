package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/workstore"
)

// FrozenReplayCandidate supplies the exact ORIGINAL applied contract for an
// explicit restore review. It does not project shared defaults into a different
// source fingerprint and does not restore any old authorization. Current draft
// observations bind the review; grant/runtime operations reuse their existing
// independent authorities and audit. No caller-supplied HTTP config is accepted.
func (m *Manager) FrozenReplayCandidate(ctx context.Context, f FrozenMCP) (ReplayDraft, error) {
	var out ReplayDraft
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		local, err := m.localProfileStore()
		if err != nil {
			return err
		}
		return local.Locked(func() error {
			if err := local.Pending(); err != nil {
				return err
			}
			if err := m.frozenCurrentIdentity(); err != nil {
				return err
			}
			report, err := m.frozenMCPReport(f)
			if err != nil {
				return err
			}
			before, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			localBefore, err := local.Capture(local.Root)
			if err != nil {
				return err
			}
			out = ReplayDraft{Intent: "restore-applied", SourceBefore: before, SourceAfter: workstore.Clone(f.Tree), LocalBefore: localBefore, LocalAfter: workstore.Clone(f.Configuration.LocalTree), Layers: f.Configuration.Layers, Report: report, Authorities: append([]string{}, f.Effective.Authorities...)}
			configuration := f.Configuration
			out.FrozenConfiguration = &configuration
			out.Authorities, out.BridgeDigest, err = m.replayAuthorities(before, f.Effective, f.Configuration)
			if err != nil {
				return err
			}
			idx, shared, err := m.load(&out.SourceAfter)
			if err != nil {
				return err
			}
			out.Preflight, err = m.preflight(out.SourceAfter, idx, shared, out.Layers)
			if err != nil {
				return err
			}
			out.Preflight.Candidate = true
			out.Digest = m.replayDraftDigest(out)
			return nil
		})
	})
	return out, err
}
