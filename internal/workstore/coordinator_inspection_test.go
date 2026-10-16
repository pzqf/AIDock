package workstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func observeDigest(t *testing.T, s *Store, root string) string {
	t.Helper()
	tree, err := s.Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	return Digest(tree)
}

func TestRecoveryInspectionReportsAllConflictingRootsWithoutPersistingCurrentBytes(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "target-complete")
	for _, index := range []int{1, 2} {
		if err := os.WriteFile(filepath.Join(c.Roots[index].Root, "body"), []byte("manual secret body"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	local := observeDigest(t, c.Blobs, c.Local)
	roots := []string{}
	for _, root := range c.Roots {
		roots = append(roots, observeDigest(t, c.Blobs, root.Root))
	}
	r, err := c.InspectRecovery()
	if err != nil || r.Status != "CONFLICT" || !r.Pending || len(r.Transactions) != 1 || len(r.Transactions[0].Targets) != 3 {
		t.Fatal(r, err)
	}
	conflicts := 0
	for i, target := range r.Transactions[0].Targets {
		if target.Status == "CONFLICT" {
			conflicts++
			if !target.Observed || len(target.Issues) == 0 {
				t.Fatal(target)
			}
			found := false
			for _, change := range target.Changes {
				if change.Path == "body" && change.Current != nil && change.Current.Hash == Hash([]byte("manual secret body")) {
					found = true
				}
			}
			if !found {
				t.Fatal("current hash missing", target)
			}
		}
		if roots[i] != observeDigest(t, c.Blobs, c.Roots[i].Root) {
			t.Fatal("inspection modified a project")
		}
	}
	if conflicts != 2 || local != observeDigest(t, c.Blobs, c.Local) {
		t.Fatal("inspection did not report all conflicts or changed Local bytes", conflicts)
	}
	bytes, _ := json.Marshal(r)
	if strings.Contains(string(bytes), "manual secret body") {
		t.Fatal("report included content")
	}
	for _, index := range []int{1, 2} {
		if err = os.WriteFile(filepath.Join(c.Roots[index].Root, "body"), []byte("before-"+c.Roots[index].Path), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r, err = c.InspectRecovery()
	if err != nil || r.Status != "READY" {
		t.Fatal(r, err)
	}
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, false)
	r, err = c.InspectRecovery()
	if err != nil || r.Pending || r.Status != "IDLE" {
		t.Fatal(r, err)
	}
}

func TestRecoveryInspectionKeepsInterruptedTemporariesAndBackups(t *testing.T) {
	for _, singleFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "single-file"}[singleFile], func(t *testing.T) {
			fixture := coordinatedFixture
			if singleFile {
				fixture = coordinatedFileFixture
			}
			c, p := fixture(t)
			coordinatedCrash(t, c, p, "target-complete")
			s := c.store(c.Roots[0].Path)
			s.active = p.ID
			tmp := filepath.Join(s.Root, s.targetTemp("body"))
			body := []byte("after")
			if singleFile {
				tmp = filepath.Join(filepath.Dir(s.Root), s.targetTemp(filepath.Base(s.Root)))
				body = []byte("\xef\xbb\xbf")
			}
			if err := os.WriteFile(tmp, body, 0600); err != nil {
				t.Fatal(err)
			}
			local := observeDigest(t, c.Blobs, c.Local)
			r, err := c.InspectRecovery()
			if err != nil || r.Status != "READY" {
				t.Fatal(r, err)
			}
			bytes, err := os.ReadFile(tmp)
			if err != nil || string(bytes) != string(body) || local != observeDigest(t, c.Blobs, c.Local) {
				t.Fatal("inspection changed a temporary or backup", err)
			}
			if err = os.WriteFile(tmp, []byte("foreign temporary"), 0600); err != nil {
				t.Fatal(err)
			}
			r, err = c.InspectRecovery()
			if err != nil || r.Status != "CONFLICT" {
				t.Fatal(r, err)
			}
			if err = os.WriteFile(tmp, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Recover(); err != nil {
				t.Fatal(err)
			}
			coordinatedMixedEqual(t, c, p.Targets, false)
		})
	}
}

func TestRecoveryInspectionCommittedCleanupPreservesExternalEdits(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "committed")
	file := filepath.Join(c.Roots[1].Root, "body")
	if err := os.WriteFile(file, []byte("post commit edit"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := c.InspectRecovery()
	if err != nil || r.Status != "CLEANUP_ONLY" {
		t.Fatal(r, err)
	}
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(file)
	if err != nil || string(bytes) != "post commit edit" {
		t.Fatal("cleanup overwrote later edit", err)
	}
}

func TestRecoveryInspectionMissingBackupBlocksWithoutChangingTargets(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "target-complete")
	s := c.store(c.Roots[0].Path)
	entry := p.Targets[0].Before.Entries["body"]
	if err := os.Remove(filepath.Join(s.bucket(), "blobs", entry.Hash)); err != nil {
		t.Fatal(err)
	}
	before := observeDigest(t, c.Blobs, c.Roots[0].Root)
	r, err := c.InspectRecovery()
	if err != nil || r.Status != "CONFLICT" || r.Transactions[0].Issues[0].Code != "BACKUP_UNAVAILABLE" {
		t.Fatal(r, err)
	}
	if before != observeDigest(t, c.Blobs, c.Roots[0].Root) {
		t.Fatal("missing backup inspection wrote project")
	}
}
