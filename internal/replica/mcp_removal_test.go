package replica

import (
	"context"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func clearRemovalReferences(t *testing.T, s *source.Manager) {
	t.Helper()
	saveSharedConfiguration(t, s, func(p map[string]any) {
		delete(p, "config_overrides")
		delete(p, "config_remove")
		delete(p, "mcp")
		delete(p, "plugins")
	}, nil)
}

func removeSharedMCP(t *testing.T, s *source.Manager, explicit bool) {
	t.Helper()
	state, err := s.MCP()
	if err != nil {
		t.Fatal(err)
	}
	r := source.MCPEdit{ExpectedSHA256: state.SHA256, Config: map[string]any{"schema_version": 1, "servers": []any{}}}
	if explicit {
		r.SharedRemovals = []string{"project-memory"}
		r.ExpectedLock = state.LockSHA256
		r.ExpectedRemovals = state.RemovalsSHA256
	}
	p, err := s.MCPPlan(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestMCPDeletionFiveCopyResolutionRetainsPublisherLocalAndUnselectedMembers(t *testing.T) {
	m, b, copies := sharedPluginCopies(t, 5)
	for _, s := range copies {
		clearRemovalReferences(t, s)
	}
	target, err := m.copySource(b[1])
	if err != nil {
		t.Fatal(err)
	}
	saveCopyProfile(t, target, map[string]any{"schema_version": 2, "name": "retained local", "description": "LOCAL_PRIVATE_RETAINED"})
	baseline(t, m, b)
	publisher, err := m.copySource(b[0])
	if err != nil {
		t.Fatal(err)
	}
	removeSharedMCP(t, publisher, true)
	id := source.SharedMCPPrefix + "project-memory"
	configurationStatusFor(t, m, b, id, "SOURCE_CHANGED")
	before := captureSources(t, copies)
	choices := map[string]string{b[1].ID + "/" + id: "source", b[2].ID + "/" + id: "target", b[3].ID + "/" + id: "skip", b[4].ID + "/" + id: "source"}
	p, err := m.Resolve(context.Background(), configurationRequest(t, m, b, choices))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 2 || !resolutionChangesMCP(p) {
		t.Fatal("explicit selection widened or used legacy skill-only qualification")
	}
	for _, target := range p.Targets {
		if target.Copy.ID != b[1].ID && target.Copy.ID != b[4].ID {
			t.Fatal("unselected target modified")
		}
		body, err := m.Control.Data(target.After.Entries[source.MCPRemovalsFile])
		if err != nil || !strings.Contains(string(body), "definition_sha256") || strings.Contains(string(body), "PRIVATE_") {
			t.Fatal("deletion body leaked configuration", err)
		}
		for _, change := range target.Changes {
			if change.Path != "mcp.yaml" && change.Path != "plugins.lock" && change.Path != source.MCPRemovalsFile {
				t.Fatal("deletion changed unrelated source", change.Path)
			}
		}
	}
	if string(data(captureSources(t, copies))) != string(data(before)) {
		t.Fatal("resolution changed a live source")
	}
	_, stored := storedFullPublicationResolutionFixture(t, m, p)
	if err = m.validateFullPublication(stored); err != nil {
		t.Fatal("keep/skip comparison members blocked the two changed members", err)
	}
	for name, mutate := range map[string]func(*fullPublicationPlan){
		"duplicate-compared-copy": func(x *fullPublicationPlan) {
			x.Resolution.Request.Targets = append(x.Resolution.Request.Targets, x.Resolution.Request.Targets[0])
		},
		"publisher-as-target": func(x *fullPublicationPlan) {
			x.Resolution.Request.Targets = append(x.Resolution.Request.Targets, x.Resolution.Source.ID)
		},
		"foreign-compared-copy": func(x *fullPublicationPlan) {
			x.Resolution.Request.Targets = append(x.Resolution.Request.Targets, workstore.ID())
		},
		"missing-changed-copy": func(x *fullPublicationPlan) { x.Resolution.Request.Targets = []string{b[2].ID, b[3].ID} },
	} {
		t.Run(name, func(t *testing.T) {
			var forged fullPublicationPlan
			if err := decode(data(stored), &forged); err != nil {
				t.Fatal(err)
			}
			mutate(&forged)
			forged.Resolution.Digest = resolutionDigest(forged.Resolution)
			forged.Digest = fullPublicationDigest(forged)
			code(t, m.validateFullPublication(forged), "FULL_PUBLICATION_INVALID")
		})
	}
	code(t, m.publicationQualification(PublicationPlan{Resolution: p}), "PUBLICATION_MCP_CONFIGURATION_REQUIRED")
	// Marker-only propagation still requires the complete MCP owner.
	markerOnly := p
	markerOnly.Targets = append([]ProposedTarget{}, p.Targets...)
	for i := range markerOnly.Targets {
		markerOnly.Targets[i].Before = workstore.Clone(markerOnly.Targets[i].After)
		delete(markerOnly.Targets[i].Before.Entries, source.MCPRemovalsFile)
	}
	if !resolutionChangesMCP(markerOnly) {
		t.Fatal("deletion metadata bypassed full publication")
	}
	file := copies[0].Store.Root + "/" + source.MCPRemovalsFile
	generatedFixtureWrite(t, copies[0].Store.Root, source.MCPRemovalsFile, []byte("{\"schema_version\":1,\"removals\":[]}\n"))
	if err = m.revalidate(context.Background(), p); err == nil {
		t.Fatal("changed deletion intent did not expire the saved resolution", file)
	}
}

func TestMissingMCPSourceStillDoesNotImplyDeletion(t *testing.T) {
	m, b, copies := sharedPluginCopies(t, 2)
	clearRemovalReferences(t, copies[0])
	removeSharedMCP(t, copies[0], false)
	id := source.SharedMCPPrefix + "project-memory"
	configurationStatusFor(t, m, b, id, "MCP_DEFINITION_UNAVAILABLE")
	before := captureSources(t, copies)
	_, err := m.Resolve(context.Background(), configurationRequest(t, m, b, map[string]string{b[1].ID + "/" + id: "source"}))
	code(t, err, "REPLICA_CONFIGURATION_BLOCKED")
	if string(data(captureSources(t, copies))) != string(data(before)) {
		t.Fatal("missing-source resolution changed a live source")
	}
}
