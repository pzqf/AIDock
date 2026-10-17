package replica

import (
	"strings"
	"testing"
)

func TestCandidateInputRejectsAmbiguousConfirmationsWithoutEcho(t *testing.T) {
	for _, body := range []string{
		`{"plan_id":"p","digest":"d","copy_id":"c","edit":{"action":"grant","confirm_permissions":false,"confirm_permissions":true,"purpose":"PRIVATE_PURPOSE_SENTINEL"}}`,
		`{"plan_id":"p","plan_id":"q","digest":"d","copy_id":"c"}`,
		`{"plan_id":"p","digest":"d","copy_id":"c","edit":{"action":"grant","confirm_permissions":null}}`,
		`{"plan_id":"p","digest":"d","copy_id":"c","edit":{"action":"grant","confirm_permissions":"true"}}`,
		`{"plan_id":"p","digest":"d","copy_id":"c","edit":{"action":"grant","command":"PRIVATE_PURPOSE_SENTINEL"}}`,
		`{"plan_id":"p","digest":"d","copy_id":"c","foreign_root":"PRIVATE_PURPOSE_SENTINEL"}`,
		`{} {}`,
		`null`,
		"plan_id: &v p\ndigest: *v\ncopy_id: c\n",
	} {
		var req CandidatePermissionRequest
		err := ParseCandidateInput([]byte(body), &req)
		code(t, err, "INVALID_INPUT")
		if strings.Contains(err.Error(), "PRIVATE_PURPOSE_SENTINEL") {
			t.Fatal("candidate parser echoed a private input")
		}
	}
	var req CandidatePermissionRequest
	err := ParseCandidateInput([]byte(`{"plan_id":"p","digest":"d","copy_id":"c","edit":{"action":"grant","confirm_permissions":true,"confirm_unisolated":false}}`), &req)
	if err != nil || !req.Edit.ConfirmPermissions || req.Edit.ConfirmUnisolated {
		t.Fatal(req, err)
	}
}
