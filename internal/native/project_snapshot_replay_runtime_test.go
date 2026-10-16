package native

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestSnapshotReplayRuntimeAndEmptyClientsKeepFilesBound(t *testing.T) {
	m, id, _ := snapshotDraftFixture(t)
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	nativeBefore := snapshotNativeTrees(t, m)
	r, err := m.ReviewSnapshotReplayPermissions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	req := SnapshotReplayCandidateRequest{Snapshot: id, Candidate: r.Candidate}
	state, err := m.ReviewSnapshotReplayRuntime(context.Background(), req)
	if err != nil || state.Snapshot != id || state.Candidate != r.Candidate || state.Runtime.AuditCount != 0 {
		t.Fatal(state, err)
	}
	clients, err := m.SnapshotReplayClientConfigs(context.Background(), req, filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Source.Store.Local))
	if err != nil || len(clients.Clients) != 0 || len(clients.Sessions) != 0 {
		t.Fatal(clients, err)
	}
	req.Candidate = strings.Repeat("0", 64)
	if _, err = m.ReviewSnapshotReplayRuntime(context.Background(), req); err == nil {
		t.Fatal("wrong candidate read accepted")
	}
	if _, err = m.SnapshotReplayClientConfigs(context.Background(), req, filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Source.Store.Local)); err == nil {
		t.Fatal("wrong candidate clients accepted")
	}
	afterSource, afterLocal := snapshotDraftTrees(t, m)
	if workstore.Digest(beforeSource) != workstore.Digest(afterSource) || workstore.Digest(beforeLocal) != workstore.Digest(afterLocal) || !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("candidate runtime/client read wrote files")
	}
}

func TestSnapshotReplayRuntimeStrictInput(t *testing.T) {
	id, digest := "11111111-1111-4111-8111-111111111111", strings.Repeat("a", 64)
	base := `"snapshot_id":"` + id + `","candidate_digest":"` + digest + `"`
	for _, raw := range []string{`null`, `{` + base + `,"candidate_digest":"duplicate"}`, `{` + base + `,"command":"PRIVATE_COMMAND"}`, `{"Snapshot_id":"` + id + `","candidate_digest":"` + digest + `"}`, `{` + base + `,"runtime":null}`, `{` + base + `,"runtime":{"probe":false}}`, `{` + base + `,"runtime":{"probe":true,"probe":false}}`, `{` + base + `,"runtime":{"probe":true,"command":"PRIVATE_COMMAND"}}`} {
		if _, err := ParseSnapshotReplayCandidateRequest([]byte(raw)); err == nil || strings.Contains(err.Error(), "PRIVATE_COMMAND") {
			t.Fatal(raw, err)
		}
		if _, err := ParseSnapshotReplayRuntimeRequest([]byte(raw)); err == nil || strings.Contains(err.Error(), "PRIVATE_COMMAND") {
			t.Fatal(raw, err)
		}
	}
	if _, err := ParseSnapshotReplayCandidateRequest([]byte(`{` + base + `}`)); err != nil {
		t.Fatal(err)
	}
	good := `{` + base + `,"runtime":{"name":"owned","probe":true,"expected_sha256":"` + digest + `","expected_review_sha256":"` + digest + `"}}`
	if r, err := ParseSnapshotReplayRuntimeRequest([]byte(good)); err != nil || !r.Runtime.Probe {
		t.Fatal(r, err)
	}
}
