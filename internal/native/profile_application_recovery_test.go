package native

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestProfileApplicationProcessHelper(t *testing.T) {
	root := os.Getenv("AIDOCK_PROFILE_PROCESS_ROOT")
	if root == "" {
		return
	}
	s, err := source.New(filepath.Join(root, ".aidock"), os.Getenv("AIDOCK_PROFILE_PROCESS_LOCAL"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	s.Copy = "22222222-2222-4222-8222-222222222222"
	m, err := New(s, root)
	if err != nil {
		t.Fatal(err)
	}
	m.applicationHook = func(phase string, n int) error {
		if phase == "target-complete" && n == 4 {
			os.Exit(37)
		}
		return nil
	}
	_, err = m.profileApplicationBatch().Apply(context.Background(), os.Getenv("AIDOCK_PROFILE_PROCESS_PLAN"), os.Getenv("AIDOCK_PROFILE_PROCESS_DIGEST"))
	t.Fatal("helper did not interrupt", err)
}

func TestProfileApplicationInterruptedRecoveryChecksAllRootsBeforeRestoring(t *testing.T) {
	m, p, sessions, configs := profileApplicationFixture(t)
	before := configurationTree(t, m.Root)
	plan := compileProfileApplicationFixture(t, m, p, sessions, configs)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileApplicationProcessHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_PROFILE_PROCESS_ROOT="+m.Root, "AIDOCK_PROFILE_PROCESS_LOCAL="+m.Source.Store.Local, "AIDOCK_PROFILE_PROCESS_PLAN="+plan.ID, "AIDOCK_PROFILE_PROCESS_DIGEST="+plan.Digest)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 37 {
		t.Fatal("expected process interruption", err)
	}
	code(t, m.profileApplicationBatch().Pending(), "NATIVE_RECOVERY_REQUIRED")
	code(t, m.connectionBatch().Pending(), "RECOVERY_CONFLICT")
	path := filepath.Join(m.Root, ".mcp.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, append(append([]byte{}, original...), []byte("\n ")...))
	conflicted := configurationTree(t, m.Root)
	_, err = m.RecoverProfileApplication()
	code(t, err, "RECOVERY_CONFLICT")
	if !reflect.DeepEqual(conflicted, configurationTree(t, m.Root)) {
		t.Fatal("recovery partially restored before checking every root")
	}
	write(t, path, original)
	reopened, err := New(m.Source, m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := reopened.RecoverProfileApplication(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("restart recovery did not restore original seven roots")
	}
	if n, err := reopened.RecoverProfileApplication(); err != nil || n != 0 {
		t.Fatal("recovery not idempotent", n, err)
	}
	if _, err = os.Stat(filepath.Join(m.Root, workstore.NativeBarrier)); !os.IsNotExist(err) {
		t.Fatal("barrier retained after recovery", err)
	}
}
