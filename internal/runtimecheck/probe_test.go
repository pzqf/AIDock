package runtimecheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		mode, _ := os.ReadFile("mode.txt")
		if os.Getenv("AIDOCK_RUNTIME_PARENT_SENTINEL") != "" || os.Getenv("NODE_OPTIONS") != "" || os.Getenv("PATH") != "" || os.Getenv("HOME") != "" || os.Getenv("USERPROFILE") != "" {
			fmt.Print("unexpected inherited environment")
			os.Exit(0)
		}
		switch string(mode) {
		case "hang":
			time.Sleep(time.Minute)
		case "mismatch":
			fmt.Print("v9.8.7\n")
		case "invalid":
			fmt.Print("private-sentinel\nv1.2.3\n")
		case "overflow":
			fmt.Print(strings.Repeat("private-sentinel", 1000))
		case "fail":
			fmt.Fprint(os.Stderr, "private-sentinel")
			os.Exit(7)
		case "descendant":
			child := exec.Command(os.Args[0], "--runtime-child")
			child.Dir = "."
			if child.Start() != nil {
				os.Exit(8)
			}
			for n := 0; n < 100; n++ {
				if _, err := os.Stat("child-ready"); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			fmt.Print("v1.2.3\n")
		default:
			fmt.Print("v1.2.3\r\n")
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--runtime-child" {
		_ = os.WriteFile("child-ready", []byte("ready"), 0600)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestFixedVersionProcessUsesMinimalEnvironmentRedactsOutputAndReaps(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIDOCK_RUNTIME_PARENT_SENTINEL", "private-sentinel")
	t.Setenv("NODE_OPTIONS", "--require=private-sentinel")
	for _, tc := range []struct{ mode, code, version string }{{"match", "RUNTIME_VERSION_MATCH", "v1.2.3"}, {"mismatch", "RUNTIME_VERSION_MISMATCH", "v9.8.7"}, {"invalid", "RUNTIME_CHECK_OUTPUT_INVALID", ""}, {"overflow", "RUNTIME_CHECK_OUTPUT_INVALID", ""}, {"fail", "RUNTIME_CHECK_FAILED", ""}, {"hang", "RUNTIME_CHECK_TIMEOUT", ""}, {"descendant", "RUNTIME_VERSION_MATCH", "v1.2.3"}} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			_, input := fixture(t)
			input.Runtime.Executable = "runtime.exe"
			if err := os.WriteFile(filepath.Join(root, "runtime.exe"), binary, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "mode.txt"), []byte(tc.mode), 0600); err != nil {
				t.Fatal(err)
			}
			code, version := NodeVersion(context.Background(), root, input)
			if code != tc.code || version != tc.version {
				t.Fatal(code, version)
			}
			if tc.mode == "descendant" {
				if _, err := os.Stat(filepath.Join(root, "child-ready")); err != nil {
					t.Fatal("child did not start", err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for {
					err := os.Rename(root, root+"-released")
					if err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("descendant or image still holds test directory", err)
					}
					time.Sleep(20 * time.Millisecond)
				}
				if err := os.Rename(root+"-released", root); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
