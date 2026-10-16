//go:build windows

package privatefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateBackupDACLIsProtectedAndInherited(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := Ensure(root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "original-config")
	if err := os.WriteFile(file, []byte("PRIVATE_TEST_VALUE"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, file} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		text := sd.String()
		if !strings.Contains(text, user.User.Sid.String()) || !strings.Contains(text, "SY") {
			t.Fatal("missing account/system access", text)
		}
		for _, other := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;BA)"} {
			if strings.Contains(text, other) {
				t.Fatal("backup readable by broader group", text)
			}
		}
		if path == root && !strings.Contains(text, "D:P") {
			t.Fatal("directory DACL still inherits broad access", text)
		}
	}
}
