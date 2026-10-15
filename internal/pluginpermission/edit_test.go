package pluginpermission

import (
	"strings"
	"testing"
)

func TestParseEditRejectsAmbiguousSecretConsentWithoutEcho(t *testing.T) {
	for _, raw := range []string{`{"confirm_secrets":true,"confirm_secrets":false}`, `{"Confirm_Secrets":true}`, `{"confirm_secrets":null}`, `{"confirm_secrets":"private-sentinel"}`, `{"private-sentinel":true}`, `{"purpose":"private-sentinel"`} {
		_, err := ParseEdit([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("ambiguous consent or leak", err)
		}
	}
	req, err := ParseEdit([]byte(`{"action":"grant","confirm_permissions":true,"confirm_secrets":true}`))
	if err != nil || !req.ConfirmSecrets || !req.ConfirmPermissions {
		t.Fatal(req, err)
	}
}
