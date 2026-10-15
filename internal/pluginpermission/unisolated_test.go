package pluginpermission

import (
	"strings"
	"testing"
)

func TestExplicitUnisolatedChoiceCannotBeInferredFromTierOrGeneralConsent(t *testing.T) {
	u := &UnisolatedInput{SourceType: "local", Kind: "node", Version: "24.0.0", Executable: "runtime/node.exe", SHA256: strings.Repeat("a", 64)}
	i := Input{Tier: 0, Unisolated: u}
	if confirmUnisolated(i, Edit{ConfirmPermissions: true}) == nil {
		t.Fatal("general permission consent implied own trust")
	}
	if err := confirmUnisolated(i, Edit{ConfirmPermissions: true, ConfirmUnisolated: true}); err != nil {
		t.Fatal(err)
	}
	u.SourceType = "npm"
	if confirmUnisolated(i, Edit{ConfirmPermissions: true, ConfirmUnisolated: true}) == nil {
		t.Fatal("community plugin accepted Tier 0 own trust")
	}
	i.Unisolated = nil
	if confirmUnisolated(i, Edit{ConfirmUnisolated: true}) == nil || ownTrusted(Record{Input: i, OwnTrusted: true}) {
		t.Fatal("legacy Tier 0 record silently gained trust")
	}
	for _, raw := range []string{`{"confirm_unisolated":true,"confirm_unisolated":false}`, `{"Confirm_Unisolated":true}`, `{"confirm_unisolated":null}`, `{"confirm_unisolated":"private-sentinel"}`} {
		_, err := ParseEdit([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal(err)
		}
	}
	r, err := ParseEdit([]byte(`{"action":"grant","confirm_permissions":true,"confirm_unisolated":true}`))
	if err != nil || !r.ConfirmUnisolated {
		t.Fatal(r, err)
	}
}
