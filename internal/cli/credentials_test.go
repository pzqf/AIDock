package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialCLIRequiresConfirmRevisionAndStdinWithoutCreatingHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	for _, tc := range []struct {
		args []string
		code string
	}{{[]string{"secret", "add", "key"}, "CONFIRMATION_REQUIRED"}, {[]string{"secret", "add", "key", "--confirm"}, "INVALID_INPUT"}, {[]string{"secret", "rotate", "key", "--confirm"}, "INVALID_INPUT"}, {[]string{"secret", "delete", "key", "--confirm"}, "CREDENTIAL_REFERENCES_STALE"}, {[]string{"secret", "reconcile"}, "CONFIRMATION_REQUIRED"}, {[]string{"secret", "add", "key", "extra-private-value"}, "INVALID_INPUT"}} {
		args := append(tc.args, "--home", home, "--json")
		exit, v, _ := invoke(t, args...)
		if exit == 0 || v["code"] != tc.code {
			t.Fatal(tc.args, exit, v)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid command created home")
		}
	}
}
