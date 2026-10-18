package replica

import (
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullPublicationOperationsStrictConsentAndIdentity(t *testing.T) {
	id, plan, copy, check := workstore.ID(), workstore.ID(), workstore.ID(), workstore.ID()
	sha := strings.Repeat("a", 64)
	base := `"publication_id":"` + id + `","plan_id":"` + plan + `","digest":"` + sha + `"`
	view := base + `,"copy_id":"` + copy + `","applied_binding":"` + sha + `"`
	candidate := view + `,"candidate_digest":"` + sha + `"`
	valid := map[string]string{
		"full-apply":                      `{"publication_id":"` + id + `","digest":"` + sha + `"}`,
		"full-execution":                  `{"publication_id":"` + id + `"}`,
		"full-inspect-recovery":           `{"publication_id":"` + id + `"}`,
		"full-recover":                    `{"publication_id":"` + id + `"}`,
		"full-restore-plan":               `{"publication_id":"` + id + `","transaction":"` + plan + `","copies":["` + copy + `"]}`,
		"full-restore-show":               `{` + base + `}`,
		"full-restore-confirm":            `{` + base + `}`,
		"full-restore-review":             `{` + view + `}`,
		"full-restore-artifact-plan":      `{` + candidate + `,"edit":{"action":"import","name":"owned-service","expected_sha256":"` + sha + `","expected_lock_sha256":"` + sha + `","archive_path":"D:/owned/exact.zip"}}`,
		"full-restore-artifact-confirm":   `{` + candidate + `,"name":"owned-service","operation_plan_id":"` + check + `","operation_digest":"` + sha + `"}`,
		"full-restore-install-plan":       `{` + candidate + `,"edit":{"action":"install","name":"owned-service","expected_sha256":"` + sha + `","allow_unverified":true,"risk_reason":"owned exact version review"}}`,
		"full-restore-install-confirm":    `{` + candidate + `,"name":"owned-service","operation_plan_id":"` + check + `","operation_digest":"` + sha + `"}`,
		"full-restore-permission-plan":    `{` + candidate + `,"authorization":{"action":"grant","name":"owned-service","confirm_permissions":true}}`,
		"full-restore-permission-confirm": `{` + candidate + `,"name":"owned-service","operation_plan_id":"` + check + `","operation_digest":"` + sha + `"}`,
		"full-restore-runtime-check":      `{` + candidate + `,"runtime":{"name":"owned-service","probe":true,"expected_sha256":"` + sha + `","expected_review_sha256":"` + sha + `"}}`,
		"full-restore-prepare":            `{` + candidate + `,"preparation":{"probe":true,"rule_probe":true}}`,
		"full-restore-prepared":           `{` + candidate + `,"preparation_id":"` + check + `"}`,
		"full-restore-regenerate":         `{` + base + `,"checks":[{"copy_id":"` + copy + `","checks":[{"applied_binding":"` + sha + `","check_id":"` + check + `","check_digest":"` + sha + `"}]}]}`,
	}
	if len(valid) != len(FullPublicationOperationNames()) {
		t.Fatal("a public operation lacks strict contract coverage")
	}
	for action, raw := range valid {
		if _, err := ParseFullPublicationOperation(action, []byte(raw)); err != nil {
			t.Fatal("valid explicit operation rejected", action, err)
		}
		for _, bad := range []string{
			strings.Replace(raw, `"publication_id":`, `"publication_id":null,"publication_id":`, 1),
			strings.Replace(raw, `"publication_id":`, `"Publication_id":`, 1),
			strings.Replace(raw, `"publication_id":`, `"source_root":"PRIVATE_UNKNOWN_INPUT","publication_id":`, 1),
			strings.Replace(raw, `"publication_id":"`+id+`"`, `"publication_id":null`, 1),
			raw + raw,
			`null`,
		} {
			_, err := ParseFullPublicationOperation(action, []byte(bad))
			code(t, err, "INVALID_INPUT")
			if strings.Contains(err.Error(), "PRIVATE_UNKNOWN_INPUT") {
				t.Fatal("private input echoed")
			}
		}
	}
	for _, tc := range []struct{ action, raw string }{
		{"unknown", valid["full-recover"]},
		{"full-restore-artifact-plan", strings.Replace(valid["full-restore-artifact-plan"], `"archive_path":`, `"snapshot":"PRIVATE_UNKNOWN_INPUT","archive_path":`, 1)},
		{"full-restore-artifact-plan", strings.Replace(valid["full-restore-artifact-plan"], `"import"`, `"rollback"`, 1)},
		{"full-restore-install-plan", strings.Replace(valid["full-restore-install-plan"], `"risk_reason":`, `"Risk_reason":`, 1)},
		{"full-restore-install-plan", strings.Replace(valid["full-restore-install-plan"], `"allow_unverified":true`, `"allow_unverified":null`, 1)},
		{"full-restore-plan", strings.Replace(valid["full-restore-plan"], `"copies":["`+copy+`"]`, `"copies":["`+copy+`","`+copy+`"]`, 1)},
		{"full-restore-plan", strings.Replace(valid["full-restore-plan"], `"copies":["`+copy+`"]`, `"copies":[]`, 1)},
		{"full-restore-permission-plan", strings.Replace(valid["full-restore-permission-plan"], `"grant"`, `"revoke"`, 1)},
		{"full-restore-permission-plan", strings.Replace(valid["full-restore-permission-plan"], `"confirm_permissions":true`, `"confirm_permissions":true,"Confirm_permissions":false`, 1)},
		{"full-restore-runtime-check", strings.Replace(valid["full-restore-runtime-check"], `"probe":true`, `"probe":false`, 1)},
		{"full-restore-prepare", strings.Replace(valid["full-restore-prepare"], `"probe":true`, `"probe":false`, 1)},
		{"full-restore-prepare", strings.Replace(valid["full-restore-prepare"], `"probe":true`, `"probe":true,"application_scope":"skills-rules"`, 1)},
		{"full-restore-regenerate", strings.Replace(valid["full-restore-regenerate"], `"check_id":`, `"Check_id":`, 1)},
		{"full-restore-regenerate", strings.Replace(valid["full-restore-regenerate"], `"checks":[{`, `"checks":[null,{`, 1)},
	} {
		_, err := ParseFullPublicationOperation(tc.action, []byte(tc.raw))
		code(t, err, "INVALID_INPUT")
	}
}
