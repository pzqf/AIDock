package replica

import (
	"strings"
	"testing"
)

func TestCandidatePreparationParserKeepsFullScopeAndRejectsAmbiguousInput(t *testing.T) {
	valid := `{"plan_id":"p","digest":"d","copy_id":"c","preparation":{"tools":["codex-cli"],"probe":false}}`
	req, err := ParseCandidatePreparationRequest([]byte(valid))
	if err != nil || req.Preparation.Probe || len(req.Preparation.Tools) != 1 || req.Preparation.Tools[0] != "codex-cli" {
		t.Fatal(req, err)
	}
	for _, body := range []string{
		strings.Replace(valid, `"probe":false`, `"probe":false,"probe":true`, 1),
		strings.Replace(valid, `"probe":false`, `"probe":null`, 1),
		strings.Replace(valid, `"probe":false`, `"probe":"true"`, 1),
		strings.Replace(valid, `"probe":false`, `"probe":false,"rule_probe":true`, 1),
		strings.Replace(valid, `"probe":false`, `"application_scope":"skills-rules"`, 1),
		strings.Replace(valid, `"probe":false`, `"command":"PRIVATE_INPUT_SENTINEL"`, 1),
		strings.Replace(valid, `["codex-cli"]`, `[null]`, 1),
		strings.Replace(valid, `"copy_id":"c"`, `"copy_id":"c","foreign_root":"PRIVATE_INPUT_SENTINEL"`, 1),
		`{"plan_id":"p","digest":"d","copy_id":"c","preparation":null}`,
		`{} {}`,
		"plan_id: &v p\ndigest: *v\ncopy_id: c\npreparation: {}\n",
	} {
		_, err := ParseCandidatePreparationRequest([]byte(body))
		code(t, err, "INVALID_INPUT")
		if strings.Contains(err.Error(), "PRIVATE_INPUT_SENTINEL") {
			t.Fatal("parser echoed private input")
		}
	}
}
