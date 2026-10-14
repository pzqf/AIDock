package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func fakeProbes(m *Manager, discover func(context.Context, toolBinary, string, string, string) (discovery, error)) {
	m.probe = &probeOps{
		resolve: func(id string) (toolBinary, error) {
			return toolBinary{Path: id + ".exe", Digest: strings.Repeat("a", 64)}, nil
		},
		version: func(_ context.Context, _ toolBinary, id, _, _ string) (string, error) {
			if id == "codex-cli" {
				return "0.162.1", nil
			}
			return "2.1.257", nil
		},
		discover: discover,
	}
}
func discoveredGuide(_ context.Context, _ toolBinary, id, cwd, _ string) (discovery, error) {
	dir := ".agents/skills"
	if id == "claude-code" {
		dir = ".claude/skills"
	}
	return discovery{Skills: []discoveredSkill{{Name: "guide", Description: "example", Enabled: true, Path: filepath.Join(cwd, filepath.FromSlash(dir), "guide", "SKILL.md")}}}, nil
}

func TestStaticToolCheckNeverStartsProcesses(t *testing.T) {
	m, _ := fixture(t)
	fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("static check started discovery")
		return discovery{}, nil
	})
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("static check started --version")
		return "", nil
	}
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || r.Probe || r.Status != "UNKNOWN" || len(r.Tools) != 2 {
		t.Fatal(r, err)
	}
	for _, tool := range r.Tools {
		if tool.L1 != "NOT_APPLIED" || tool.L2 != "UNKNOWN" || tool.L3 != "UNKNOWN" || tool.L4 != "UNKNOWN" {
			t.Fatal(tool)
		}
		if _, err := os.Stat(tool.Target); !os.IsNotExist(err) {
			t.Fatal("created tool target")
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	last, err := m.Latest(context.Background())
	if err != nil || last == nil || last.ID != r.ID || last.Stale {
		t.Fatal(last, err)
	}
}

func TestProbeUsesCompleteIsolatedPackageAndStoresFreshness(t *testing.T) {
	m, id := fixture(t)
	calls := 0
	fakeProbes(m, func(ctx context.Context, b toolBinary, tool, cwd, home string) (discovery, error) {
		calls++
		if !workstore.Within(m.Source.Store.Local, cwd) || cwd == m.Root || !workstore.Within(m.Source.Store.Local, home) {
			t.Fatal("probe escaped Local Root")
		}
		dir := ".agents/skills"
		if tool == "claude-code" {
			dir = ".claude/skills"
		}
		pkg := filepath.Join(cwd, filepath.FromSlash(dir), "guide")
		bytes, err := os.ReadFile(filepath.Join(pkg, "assets/data.bin"))
		if err != nil || string(bytes) != string([]byte{0, 1, 255}) {
			t.Fatal("binary lost", err)
		}
		info, err := os.Stat(filepath.Join(pkg, "empty"))
		if err != nil || !info.IsDir() {
			t.Fatal("empty directory lost")
		}
		bytes, err = os.ReadFile(filepath.Join(pkg, "SKILL.md"))
		if err != nil || !strings.Contains(string(bytes), "allowed-tools: Read") {
			t.Fatal("frontmatter lost")
		}
		if _, err := os.Stat(filepath.Join(cwd, ".git/HEAD")); err != nil {
			t.Fatal("missing parent discovery boundary")
		}
		return discoveredGuide(ctx, b, tool, cwd, home)
	})
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil || calls != 2 {
		t.Fatal(r, calls, err)
	}
	for _, tool := range r.Tools {
		if tool.L2 != "PASS" || tool.L3 != "UNKNOWN" || tool.L4 != "UNKNOWN" || tool.Skills[0].Status != "PASS" {
			t.Fatal(tool)
		}
		if _, err := os.Stat(tool.Target); !os.IsNotExist(err) {
			t.Fatal("real target created")
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	n, err := New(m.Source, m.Root)
	if err != nil {
		t.Fatal(err)
	}
	n.probe = m.probe
	last, err := n.Latest(context.Background())
	if err != nil || last == nil || last.Stale || last.Digest != r.Digest {
		t.Fatal(last, err)
	}
	applySource(t, m.Source, source.Request{Action: "disable", ID: id})
	last, err = n.Latest(context.Background())
	if err != nil || !last.Stale {
		t.Fatal("source change didn't expire evidence", last, err)
	}
	if last.Tools[0].L2 != "PASS" {
		t.Fatal("historical observation overwritten")
	}
	entries, err := os.ReadDir(filepath.Join(m.Source.Store.Local, "native-probes"))
	if err != nil || len(entries) != 0 {
		t.Fatal("probe resources retained", err)
	}
}

func TestDiscoveryRejectsWrongPathDisabledMissingDuplicateAndParseErrors(t *testing.T) {
	for _, scenario := range []string{"wrong-path", "disabled", "missing", "duplicate", "parse-error"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := fixture(t)
			fakeProbes(m, func(ctx context.Context, b toolBinary, id, cwd, home string) (discovery, error) {
				d, _ := discoveredGuide(ctx, b, id, cwd, home)
				switch scenario {
				case "wrong-path":
					d.Skills[0].Path = filepath.Join(home, "SKILL.md")
				case "disabled":
					d.Skills[0].Enabled = false
				case "missing":
					d.Skills = nil
				case "duplicate":
					d.Skills = append(d.Skills, d.Skills[0])
				case "parse-error":
					d.Errors = 1
				}
				return d, nil
			})
			r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
			if err != nil || r.Status != "FAIL" || r.Tools[0].L2 != "FAIL" {
				t.Fatal(r, err)
			}
		})
	}
}

func TestUnsupportedVersionFailedProtocolAndBlockedTargetDoNotPass(t *testing.T) {
	for _, scenario := range []string{"unsupported", "failure", "blocked", "uninstalled"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := fixture(t)
			calls := 0
			fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
				calls++
				return discovery{}, fmt.Errorf("protocol unavailable")
			})
			if scenario == "unsupported" {
				m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) { return "9.9.9", nil }
			}
			if scenario == "blocked" {
				write(t, filepath.Join(m.Root, ".agents/skills/guide/SKILL.md"), []byte("manual"))
			}
			if scenario == "uninstalled" {
				m.probe.resolve = func(string) (toolBinary, error) { return toolBinary{}, fmt.Errorf("uninstalled") }
			}
			r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
			if err != nil || r.Tools[0].L2 == "PASS" {
				t.Fatal(r, err)
			}
			if scenario == "failure" && calls != 1 || scenario != "failure" && calls != 0 {
				t.Fatal("unsupported/blocked tool started", calls)
			}
		})
	}
}

func TestCheckTargetBinaryTimeAndRecordIntegrity(t *testing.T) {
	for _, change := range []string{"target", "binary", "expiry", "tamper"} {
		t.Run(change, func(t *testing.T) {
			m, _ := fixture(t)
			fakeProbes(m, discoveredGuide)
			r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "target":
				write(t, filepath.Join(m.Root, ".agents/skills/other/notes.txt"), []byte("manual"))
			case "binary":
				m.probe.resolve = func(string) (toolBinary, error) { return toolBinary{"codex-cli.exe", strings.Repeat("b", 64)}, nil }
			case "expiry":
				r.Expires = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				r.Digest = checkDigest(r)
				write(t, filepath.Join(m.checksDir(), "reports", r.ID+".json"), jsonData(r))
			case "tamper":
				r.Tools[0].L3 = "PASS"
				write(t, filepath.Join(m.checksDir(), "reports", r.ID+".json"), jsonData(r))
			}
			last, err := m.Latest(context.Background())
			if change == "tamper" {
				code(t, err, "CHECK_CORRUPT")
			} else if err != nil || !last.Stale {
				t.Fatal(last, err)
			}
		})
	}
}

func TestProbeRejectsConcurrentSavedInputChange(t *testing.T) {
	m, id := fixture(t)
	fakeProbes(m, func(ctx context.Context, b toolBinary, idTool, cwd, home string) (discovery, error) {
		applySource(t, m.Source, source.Request{Action: "disable", ID: id})
		return discoveredGuide(ctx, b, idTool, cwd, home)
	})
	_, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	code(t, err, "PLAN_STALE")
	last, err := m.Latest(context.Background())
	if err != nil || last != nil {
		t.Fatal("saved stale check", last, err)
	}
}

func TestLocalDiscoveryIntegration(t *testing.T) {
	if os.Getenv("AIDOCK_NATIVE_PROBE_TEST") != "1" {
		t.Skip("opt-in local CLI integration; isolated fixture only")
	}
	m, _ := fixture(t)
	m.probe = &probeOps{resolve: resolveBinary, version: func(ctx context.Context, b toolBinary, id, cwd, home string) (string, error) {
		v, err := toolVersion(ctx, b, id, cwd, home)
		if err != nil {
			t.Logf("%s version protocol: %v", id, err)
		}
		return v, err
	}, discover: func(ctx context.Context, b toolBinary, id, cwd, home string) (discovery, error) {
		d, err := probeProcess(ctx, b, id, cwd, home)
		if err != nil {
			t.Logf("%s discovery protocol: %v", id, err)
		}
		return d, err
	}}
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range r.Tools {
		if tool.L2 != "PASS" || tool.L1 != "NOT_APPLIED" || tool.L3 != "UNKNOWN" {
			t.Fatalf("%+v", tool)
		}
		if _, err := os.Stat(tool.Target); !os.IsNotExist(err) {
			t.Fatal("created real tool target")
		}
		t.Logf("%s %s: isolated discovery PASS; real target NOT_APPLIED; L3 UNKNOWN", tool.Tool, tool.Version)
	}
	unchanged(t, m, m.Source.Store.Root, before)
	last, err := m.Latest(context.Background())
	if err != nil || last.Stale {
		t.Fatal(last, err)
	}
}
