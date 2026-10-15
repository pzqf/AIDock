package native

import (
	"testing"

	"github.com/pzqf/AIDock/internal/inventory"
)

func TestDraftSemanticsPreservedUnregisteredRulesDoNotWaiveFailures(t *testing.T) {
	for _, item := range []struct {
		code, status string
		blocked      bool
	}{
		{"RULE_FILES_UNMANAGED", "WARN", false},
		{"RULE_FILES_UNMANAGED", "FAIL", true},
		{"RULE_FILES_UNMANAGED", "UNKNOWN", true},
		{"PROFILE_NATIVE_UNSUPPORTED", "WARN", true},
	} {
		t.Run(item.code+"/"+item.status, func(t *testing.T) {
			codes := DraftSemantics(Target{Findings: []inventory.Finding{{Code: item.code, Status: item.status}}})
			if (len(codes) != 0) != item.blocked {
				t.Fatal("incorrect native semantic gate", codes)
			}
		})
	}
}
