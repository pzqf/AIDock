package native

import (
	"strings"
	"testing"
)

func TestRestoreTargetParsersBindEveryStageAndRejectAmbiguousControl(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	restore := map[string]any{"plan_id": id, "digest": strings.Repeat("b", 64), "applied_binding": strings.Repeat("c", 64)}
	base := map[string]any{"snapshot_id": id, "candidate_digest": strings.Repeat("a", 64), "restore": restore}
	candidate, err := ParseSnapshotReplayCandidateRequest(jsonData(base))
	if err != nil || candidate.Restore == nil || candidate.Restore.Plan != id {
		t.Fatal(candidate, err)
	}
	preparation := map[string]any{}
	for k, v := range base {
		preparation[k] = v
	}
	preparation["preparation"] = map[string]any{}
	parsed, err := ParseSnapshotReplayPreparationRequest(jsonData(preparation))
	if err != nil || parsed.Restore == nil {
		t.Fatal(parsed, err)
	}
	reviewID, target, err := ParseSnapshotReplayReviewRequest(jsonData(map[string]any{"snapshot_id": id, "restore": restore}))
	if err != nil || reviewID != id || target == nil {
		t.Fatal(reviewID, target, err)
	}
	permission := map[string]any{}
	for k, v := range base {
		permission[k] = v
	}
	permission["authorization"] = map[string]any{"action": "grant", "name": "owned-service", "expected_sha256": strings.Repeat("a", 64), "expected_review_sha256": strings.Repeat("b", 64), "confirm_permissions": true, "purpose": "explicit restore fixture", "data_level": "Public", "destination": "unknown-external-client"}
	grant, err := ParseSnapshotReplayPermissionRequest(jsonData(permission))
	if err != nil || grant.Restore == nil {
		t.Fatal(grant, err)
	}
	runtime := map[string]any{}
	for k, v := range base {
		runtime[k] = v
	}
	runtime["runtime"] = map[string]any{"name": "owned-service", "probe": true, "expected_sha256": strings.Repeat("a", 64), "expected_review_sha256": strings.Repeat("b", 64)}
	version, err := ParseSnapshotReplayRuntimeRequest(jsonData(runtime))
	if err != nil || version.Restore == nil {
		t.Fatal(version, err)
	}
	for _, bad := range []any{nil, map[string]any{}, map[string]any{"Plan_id": id, "digest": strings.Repeat("b", 64), "applied_binding": strings.Repeat("c", 64)}, map[string]any{"plan_id": id, "digest": strings.Repeat("b", 64), "applied_binding": "PRIVATE_SHOULD_NOT_ECHO"}} {
		base["restore"], preparation["restore"], permission["restore"], runtime["restore"] = bad, bad, bad, bad
		for _, parse := range []func() error{func() error { _, e := ParseSnapshotReplayCandidateRequest(jsonData(base)); return e }, func() error { _, e := ParseSnapshotReplayPreparationRequest(jsonData(preparation)); return e }, func() error { _, e := ParseSnapshotReplayPermissionRequest(jsonData(permission)); return e }, func() error { _, e := ParseSnapshotReplayRuntimeRequest(jsonData(runtime)); return e }, func() error {
			_, _, e := ParseSnapshotReplayReviewRequest(jsonData(map[string]any{"snapshot_id": id, "restore": bad}))
			return e
		}} {
			if e := parse(); e == nil || strings.Contains(e.Error(), "PRIVATE_SHOULD_NOT_ECHO") {
				t.Fatal("ambiguous restore target accepted or echoed", e)
			}
		}
	}
}
