package native

import (
	"context"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestSnapshotReplayPermissionReviewKeepsFrozenAndCurrentFilesSeparate(t *testing.T) {
	m, id, _ := snapshotDraftFixture(t)
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	nativeBefore := snapshotNativeTrees(t, m)
	r, err := m.ReviewSnapshotReplayPermissions(context.Background(), id)
	if err != nil || r.Snapshot != id || r.Project != m.Source.Owner || r.Copy != m.Source.Copy || !validDigest.MatchString(r.Candidate) || len(r.Entries) != 0 {
		t.Fatal(r, err)
	}
	if strings.Contains(string(jsonData(r)), "PRIVATE_") {
		t.Fatal("read exposed frozen/current profile or source bodies")
	}
	checkSource, checkLocal := snapshotDraftTrees(t, m)
	if workstore.Digest(beforeSource) != workstore.Digest(checkSource) || workstore.Digest(beforeLocal) != workstore.Digest(checkLocal) || !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("candidate review wrote project/local/native files")
	}
	_, err = m.PlanSnapshotReplayPermission(context.Background(), SnapshotReplayPermissionRequest{Snapshot: id, Candidate: strings.Repeat("0", 64), Authorization: pluginpermission.Edit{Action: "grant"}})
	if err == nil {
		t.Fatal("unreviewed candidate digest accepted")
	}
	if _, err = m.ReviewSnapshotReplayPermissions(context.Background(), "not-an-id"); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
}

func TestSnapshotReplayPermissionStrictRequestRejectsAmbiguousConsent(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	digest := strings.Repeat("a", 64)
	for _, raw := range []string{`null`, `{"snapshot_id":"x","snapshot_id":"y"}`, `{"Snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{}}`, `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"grant","confirm_tools":true,"confirm_tools":false}}`, `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"grant","Confirm_tools":true}}`, `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"grant","confirm_permissions":null}}`, `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"rollback"}}`, `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"grant"},"root":"PRIVATE_OVERRIDE"}`} {
		if _, err := ParseSnapshotReplayPermissionRequest([]byte(raw)); err == nil || strings.Contains(err.Error(), "PRIVATE_OVERRIDE") {
			t.Fatal("bad request accepted or echoed", raw, err)
		}
	}
	good := `{"snapshot_id":"` + id + `","candidate_digest":"` + digest + `","authorization":{"action":"grant","name":"owned-service","confirm_permissions":true}}`
	if r, err := ParseSnapshotReplayPermissionRequest([]byte(good)); err != nil || r.Authorization.Name != "owned-service" {
		t.Fatal(r, err)
	}
}
