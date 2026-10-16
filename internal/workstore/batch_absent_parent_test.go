package workstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchAbsentNoopFileDoesNotOwnParentAndLegacyRollbackRemainsUsable(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy-parent-claim"}[legacy], func(t *testing.T) {
			b, seed := batchFixture(t)
			b.Paths = []string{".agents/skills", "optional/config.json"}
			b.FilePaths = []string{"optional/config.json"}
			absent := Tree{Entries: map[string]Entry{}}
			p, err := b.Preview("apply", []BatchTarget{{Path: ".agents/skills", Before: absent, After: seed.Targets[0].After}, {Path: "optional/config.json", File: true, Before: absent, After: absent}})
			if err != nil {
				t.Fatal(err)
			}
			for _, parent := range p.Parents {
				if parent == "optional" {
					t.Fatal("no-op absence acquired parent ownership")
				}
			}
			if legacy {
				// A prior release recorded this missing parent even though the
				// absent desired file never caused it to be created.
				p.Parents = append(p.Parents, "optional")
				p.Digest = batchDigest(p)
				if err = b.writeJSON(filepath.Join(b.dir(), "plans", p.ID+".json"), p); err != nil {
					t.Fatal(err)
				}
			}
			j, err := b.Apply(context.Background(), p.ID, p.Digest)
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 3; n++ {
				if _, err = os.Stat(filepath.Join(b.Boundary, "optional")); !os.IsNotExist(err) {
					t.Fatal("optional parent was created", err)
				}
				r, err := b.Reverse(j.Plan.ID)
				if err != nil {
					t.Fatal(err)
				}
				j, err = b.Apply(context.Background(), r.ID, r.Digest)
				if err != nil {
					t.Fatal("rollback or undo failed", err)
				}
			}
			if _, err = os.Stat(filepath.Join(b.Boundary, ".agents")); !os.IsNotExist(err) {
				t.Fatal("created skill parent retained after rollback", err)
			}
		})
	}
}
