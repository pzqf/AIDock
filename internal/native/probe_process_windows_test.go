//go:build windows

package native

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeProcessHelper(t *testing.T) {
	role := os.Getenv("AIDOCK_LIFECYCLE_HELPER")
	if role == "" {
		return
	}
	if role == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestProbeProcessHelper$")
		child.Env = append(os.Environ(), "AIDOCK_LIFECYCLE_HELPER=child")
		child.Dir = os.Getenv("AIDOCK_LIFECYCLE_DIRECTORY")
		child.Stdout, child.Stderr = io.Discard, io.Discard
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
	} else {
		if err := os.WriteFile(filepath.Join(os.Getenv("AIDOCK_LIFECYCLE_DIRECTORY"), "child-ready"), []byte("ready"), 0600); err != nil {
			os.Exit(4)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestProbeLifecycleReapsDescendantsAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "normal"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "project")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProbeProcessHelper$")
			c.Dir = dir
			c.Env = append(os.Environ(), "AIDOCK_LIFECYCLE_HELPER=parent", "AIDOCK_LIFECYCLE_DIRECTORY="+dir)
			c.Stdout, c.Stderr = io.Discard, io.Discard
			stop, err := startProbe(c)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err = os.Stat(filepath.Join(dir, "child-ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("descendant did not start")
				}
				time.Sleep(15 * time.Millisecond)
			}
			if cancelled {
				cancel()
				_ = c.Wait()
				if err = stop(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = stop(); err != nil {
					t.Fatal(err)
				}
				_ = c.Wait()
			}
			if err = os.Rename(dir, dir+"-released"); err != nil {
				t.Fatalf("owned descendant still holds cwd: %v", err)
			}
		})
	}
}
