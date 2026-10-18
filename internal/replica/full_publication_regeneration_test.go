package replica

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullRegeneratedRestorationCannotWidenOriginalRole(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		t.Fatal(err)
	}
	// This is a deliberately unqualified kernel fixture. It tests original
	// common proof/selection integrity; no formal publication owner is bypassed.
	if _, err = c.Apply(context.Background(), p.Proof.Transaction.ID, p.Proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	selected := p.Proof.Members[0].Copy
	preview, err := m.PlanFullPublicationRestoration(context.Background(), p.ID, p.ID, []string{selected})
	if err != nil {
		t.Fatal(err)
	}
	seed, _, err := m.loadFullRestoration(p, preview.ID, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var r fullPublicationRestoration
	if err = decode(data(seed), &r); err != nil {
		t.Fatal(err)
	}
	r.Regenerated = &fullPublicationRegeneration{Seed: seed.Transaction.ID, SeedSHA: seed.Digest, Executable: filepath.Join(t.TempDir(), "aidock.exe"), Home: filepath.Dir(m.Source.Store.Local), Members: []FullPublicationRestorationChecks{{Copy: selected, Checks: []native.PublicationRestorationCheck{{Binding: strings.Repeat("a", 64), ID: workstore.ID(), Digest: strings.Repeat("b", 64)}}}}}
	if err = m.validateFullRegeneratedRole(seed, r); err != nil {
		t.Fatal("unchanged role was not structurally valid", err)
	}
	prefix := projectsnapshot.PublicationMemberPrefix(selected)
	for _, path := range []string{"control", prefix + "source", prefix + "local", prefix + "native/.agents", prefix + "native/CLAUDE.md", prefix + "native/.codex"} {
		t.Run(path, func(t *testing.T) {
			var forged fullPublicationRestoration
			if err := decode(data(r), &forged); err != nil {
				t.Fatal(err)
			}
			found := false
			for i, target := range forged.Transaction.Targets {
				if target.Path != path {
					continue
				}
				found = true
				forged.Transaction.Targets[i].After.Exists = true
				forged.Transaction.Targets[i].After.Entries["unrelated.txt"] = workstore.Entry{Hash: workstore.Hash([]byte("foreign mutation")), Size: 16, Mode: 0600}
			}
			if !found {
				t.Fatal("missing fixture role", path)
			}
			forged.Transaction.Digest = workstore.BatchPlanDigest(forged.Transaction)
			forged.Digest = fullRestorationDigest(forged)
			code(t, m.validateFullRegeneratedRole(seed, forged), "FULL_PUBLICATION_INVALID")
		})
	}
	for _, change := range []func(*fullPublicationRestoration){
		func(r *fullPublicationRestoration) { r.Regenerated.Members[0].Copy = p.Proof.Publisher.Copy },
		func(r *fullPublicationRestoration) {
			r.Regenerated.Members = append(r.Regenerated.Members, r.Regenerated.Members[0])
		},
		func(r *fullPublicationRestoration) { r.Copies = append(r.Copies, workstore.ID()) },
		func(r *fullPublicationRestoration) { r.Transaction.Reference = workstore.ID() },
		func(r *fullPublicationRestoration) {
			r.Regenerated.Members[0].Checks = append(r.Regenerated.Members[0].Checks, r.Regenerated.Members[0].Checks[0])
		},
		func(r *fullPublicationRestoration) {
			r.Transaction.Targets[0].Before.Exists = !r.Transaction.Targets[0].Before.Exists
		},
	} {
		var forged fullPublicationRestoration
		if err := decode(data(r), &forged); err != nil {
			t.Fatal(err)
		}
		change(&forged)
		forged.Transaction.Digest = workstore.BatchPlanDigest(forged.Transaction)
		forged.Digest = fullRestorationDigest(forged)
		code(t, m.validateFullRegeneratedRole(seed, forged), "FULL_PUBLICATION_INVALID")
	}
	_, err = m.FullPublicationRestoreCandidate(context.Background(), p.ID, seed.Transaction.ID, seed.Digest, p.Proof.Publisher.Copy, strings.Repeat("a", 64))
	code(t, err, "REPLAY_COPY_SELECTION_INVALID")
	_, err = m.PlanRegeneratedFullPublicationRestoration(context.Background(), p.ID, seed.Transaction.ID, seed.Digest, nil, r.Regenerated.Executable, r.Regenerated.Home)
	code(t, err, "FULL_PUBLICATION_INVALID")
}
