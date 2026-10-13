//go:build !windows

package source

import (
	"os"
	"testing"
)

func TestExternalExecutableChangeKeepsBothApprovedModes(t *testing.T) {
	m := manager(t)
	_, binary := externalFixture(t, m)
	first := apply(t, m, adoption(t, m))
	old := current(t, m).External[0]
	if err := os.Chmod(binary, 0755); err != nil {
		t.Fatal(err)
	}
	apply(t, m, adoption(t, m))
	now := current(t, m).External[0]
	if old.Skill.Revision == now.Skill.Revision || len(current(t, m).ExternalHistory) != 2 {
		t.Fatal("executable change reused immutable cache")
	}
	apply(t, m, Request{Action: "external-select", ID: first.SkillID, Approval: old.ApprovalID})
	pkg, err := m.cached(nil, effective(t, m, first.SkillID).Skill)
	if err != nil || pkg.Entries["assets/data.bin"].Mode&0111 != 0 {
		t.Fatal("old executable mode changed", err)
	}
}
