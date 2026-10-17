package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestReplayRequestRequiresAnExplicitTargetPhaseAndConfirmation(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	sha := strings.Repeat("a", 64)
	for _, raw := range []string{
		`{"snapshot_id":"` + id + `"}`,
		`{"snapshot_id":"` + id + `","kind":"replica","copies":["` + id + `"]}`,
		`{"snapshot_id":"` + id + `","kind":"project","action":"plan","prepared_id":"` + id + `","digest":"` + sha + `"}`,
		`{"snapshot_id":"` + id + `","action":"planned","plan_id":"` + id + `","digest":"` + sha + `"}`,
		`{"snapshot_id":"` + id + `","action":"apply","plan_id":"` + id + `","digest":"` + sha + `","confirm":true}`,
	} {
		if _, err := ParseReplayRequest([]byte(raw)); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"snapshot_id":null}`, `{"snapshot_id":"x"}`,
		`{"snapshot_id":"` + id + `","snapshot_id":"` + id + `"}`,
		`{"Snapshot_id":"` + id + `"}`, `{"snapshot_id":"` + id + `","native_root":"PRIVATE_NO_ECHO"}`,
		`{"snapshot_id":"` + id + `","copies":null}`, `{"snapshot_id":"` + id + `","copies":[]}`,
		`{"snapshot_id":"` + id + `","copies":["` + id + `","` + id + `"]}`,
		`{"snapshot_id":"` + id + `","kind":"project","copies":["` + id + `"]}`,
		`{"snapshot_id":"` + id + `","kind":"replica","action":"review"}`,
		`{"snapshot_id":"` + id + `","kind":"replica","action":"plan","prepared_id":"` + id + `","digest":"` + sha + `"}`,
		`{"snapshot_id":"` + id + `","action":"apply","plan_id":"` + id + `","digest":"` + sha + `"}`,
		`{"snapshot_id":"` + id + `","action":"apply","plan_id":"` + id + `","digest":"` + sha + `","confirm":false}`,
		`{"snapshot_id":"` + id + `","action":"apply","plan_id":"` + id + `","digest":"` + sha + `","confirm":"true"}`,
		`{"snapshot_id":"` + id + `","action":"planned","plan_id":"` + id + `","digest":"` + sha + `","confirm":true}`,
		`{"snapshot_id":"` + id + `","action":"planned","plan_id":"` + id + `","digest":"` + sha + `","copies":["` + id + `"]}`,
	} {
		if _, err := ParseReplayRequest([]byte(raw)); err == nil || strings.Contains(err.Error(), "PRIVATE_NO_ECHO") {
			t.Fatal(fmt.Sprintf("unsafe replay request accepted/echoed: %s", raw), err)
		}
	}
}
