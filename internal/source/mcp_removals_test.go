package source

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func explicitMCPRemoval(t *testing.T, m *Manager) MCPPreview {
	t.Helper()
	if m.Copy == "" {
		m.Copy = workstore.ID()
	}
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, ExpectedLock: s.LockSHA256, ExpectedRemovals: s.RemovalsSHA256, SharedRemovals: []string{"project-memory"}, Config: map[string]any{"schema_version": 1, "servers": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMCPExplicitRemovalPreviewRollbackAndReintroduction(t *testing.T) {
	m, name := permissionFixture(t)
	m.Copy = workstore.ID()
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	before := capture(t, m)
	original, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	installBefore, err := m.MCPInstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	permissionBefore, err := m.MCPPermission(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := mcpSharedGroup(t, m)
	p := explicitMCPRemoval(t, m)
	unchangedManager(t, m, before)
	if len(p.Candidate.Servers) != 0 || len(p.Candidate.Removals) != 1 || p.Candidate.Removals[0].DefinitionSHA256 != pluginlock.Definition(old.MCP.Definition) || p.Candidate.Removals[0].LockSHA256 != workstore.Hash(jsonBytes(old.MCP.Lock)) {
		t.Fatal("deletion lost the exact original identity", p.Candidate.Removals)
	}
	paths := map[string]bool{}
	for _, c := range p.Plan.Changes {
		paths[c.Path] = true
	}
	if len(paths) != 3 || !paths["mcp.yaml"] || !paths["plugins.lock"] || !paths[MCPRemovalsFile] {
		t.Fatal(paths)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	deleted := capture(t, m)
	group := mcpSharedGroup(t, m)
	if group.MCP.Removal == nil || group.MCP.Lock != nil || group.MCP.Definition != nil {
		t.Fatal("deletion reappeared as an active contract", group)
	}
	installAfter, err := m.MCPInstall(context.Background())
	if err != nil || installAfter.SHA256 != installBefore.SHA256 || installAfter.AuditCount != installBefore.AuditCount {
		t.Fatal("source removal changed installation authority", err)
	}
	permissionAfter, err := m.MCPPermission(context.Background())
	if err != nil || permissionAfter.SHA256 != permissionBefore.SHA256 || permissionAfter.AuditCount != permissionBefore.AuditCount {
		t.Fatal("source removal changed permission authority", err)
	}
	r := apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	unchangedManager(t, m, before)
	apply(t, m, Request{Action: "rollback", Snapshot: r.Plan.ID})
	unchangedManager(t, m, deleted)
	current, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	readd := MCPEdit{ExpectedSHA256: current.SHA256, Config: original.Config}
	_, err = m.MCPPlan(context.Background(), readd)
	code(t, err, "MCP_REMOVAL_CONFIRMATION_REQUIRED")
	readd.ExpectedRemovals = current.RemovalsSHA256
	added, err := m.MCPPlan(context.Background(), readd)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := added.Plan.After.Entries[MCPRemovalsFile]; ok {
		t.Fatal("reintroduction retained an active deletion")
	}
	lock, err := m.mcpLockState(context.Background(), added.Plan.After)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pluginlock.Parse([]byte(lock.Document))
	if err != nil || len(parsed.Plugins) != 0 {
		t.Fatal("reintroduction silently restored the old lock", err)
	}
}

func TestMCPExplicitRemovalRequiresObservedDigestsAndActualRemovedName(t *testing.T) {
	m, _ := installFixture(t)
	m.Copy = workstore.ID()
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	before := capture(t, m)
	valid := MCPEdit{ExpectedSHA256: s.SHA256, ExpectedLock: s.LockSHA256, ExpectedRemovals: s.RemovalsSHA256, SharedRemovals: []string{"project-memory"}, Config: map[string]any{"schema_version": 1, "servers": []any{}}}
	for _, test := range []struct {
		name, code string
		edit       func(*MCPEdit)
	}{
		{"missing-lock", "MCP_REMOVAL_CONFIRMATION_REQUIRED", func(r *MCPEdit) { r.ExpectedLock = "" }},
		{"missing-marker", "MCP_REMOVAL_CONFIRMATION_REQUIRED", func(r *MCPEdit) { r.ExpectedRemovals = "" }},
		{"stale-lock", "PLAN_STALE", func(r *MCPEdit) { r.ExpectedLock = strings.Repeat("a", 64) }},
		{"stale-marker", "PLAN_STALE", func(r *MCPEdit) { r.ExpectedRemovals = strings.Repeat("a", 64) }},
		{"unknown", "INVALID_INPUT", func(r *MCPEdit) { r.SharedRemovals = []string{"foreign"} }},
		{"duplicate", "INVALID_INPUT", func(r *MCPEdit) { r.SharedRemovals = []string{"project-memory", "project-memory"} }},
		{"still-active", "INVALID_INPUT", func(r *MCPEdit) { r.Config = s.Config }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := valid
			test.edit(&r)
			_, err := m.MCPPlan(context.Background(), r)
			code(t, err, test.code)
			unchangedManager(t, m, before)
		})
	}
	boundCopy := m.Copy
	m.Copy = ""
	_, err = m.MCPPlan(context.Background(), valid)
	code(t, err, "COPY_BINDING_REQUIRED")
	m.Copy = boundCopy
	// Removing an ordinary declaration remains distinct from an explicit,
	// independently selected cross-copy deletion, including on older clients.
	ordinary := valid
	ordinary.SharedRemovals = nil
	p, err := m.MCPPlan(context.Background(), ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Candidate.Removals) != 0 {
		t.Fatal("ordinary absence became a propagating deletion")
	}
	for _, raw := range []string{`{"shared_removals":null}`, `{"shared_removals":["x","x"]}`, `{"shared_removals":["x"],"shared_removals":[]}`, `{"shared_removals":[1]}`, `{"expected_lock_sha256":null}`, `{"shared_removals":["../foreign"]}`} {
		_, err := ParseMCPEdit([]byte(raw))
		code(t, err, "INVALID_INPUT")
	}
}

func TestSharedMCPDeletionRequiresReferenceCoordinationAndPreservesLocal(t *testing.T) {
	src, _ := installFixture(t)
	dst, _ := installFixture(t)
	p := explicitMCPRemoval(t, src)
	if _, err := src.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	removal := mcpSharedGroup(t, src)
	dst.Copy = "owned-removal-copy"
	saveMCPProfile(t, dst, "disable", "")
	before := capture(t, dst)
	_, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{removal}, src.Store)
	code(t, err, "MCP_PROFILE_REFERENCE_EXISTS")
	catalog, err := src.Shared()
	if err != nil {
		t.Fatal(err)
	}
	selected := []SharedConfiguration{removal}
	for _, g := range catalog.Configurations {
		if g.ID == SharedProfileID {
			selected = append(selected, g)
		}
	}
	after, _, err := dst.DraftSharedConfiguration(before, nil, selected, src.Store)
	if err != nil {
		t.Fatal("explicit shared Profile coordination should work", err)
	}
	next, err := dst.mcpConfig(after)
	if err != nil || len(array(next["servers"])) != 0 {
		t.Fatal(next, err)
	}
	markers, err := dst.mcpRemovals(after)
	if err != nil || len(markers) != 1 || markers[0] != *removal.MCP.Removal {
		t.Fatal(markers, err)
	}
	for path, entry := range before.Entries {
		if path != "mcp.yaml" && path != "plugins.lock" && path != "profile.yaml" && after.Entries[path] != entry {
			t.Fatal("unrelated source changed", path)
		}
	}
	unchangedManager(t, dst, before)
	saveLocal(t, dst, ProfileEdit{Profile: mcpProfile("local-reference", "disable")})
	local, err := dst.localProfileStore()
	if err != nil {
		t.Fatal(err)
	}
	localBefore, err := local.Capture(local.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = dst.DraftSharedConfiguration(before, nil, selected, src.Store)
	code(t, err, "MCP_PROFILE_REFERENCE_EXISTS")
	localAfter, err := local.Capture(local.Root)
	if err != nil || workstore.Digest(localAfter) != workstore.Digest(localBefore) {
		t.Fatal("local reference was silently erased", err)
	}
}

func TestMCPRemovalCorruptionDoesNotBecomeAnEmptyOrActiveSharedUnit(t *testing.T) {
	m, _ := installFixture(t)
	for _, body := range []string{`{"schema_version":1,"removals":[{"name":"PRIVATE_SENTINEL","definition_sha256":"bad","lock_sha256":"bad"}]}`, `{"schema_version":1,"removals":null}`, `{"schema_version":1,"removals":[],"unknown":"PRIVATE_SENTINEL"}`} {
		write(t, filepath.Join(m.Store.Root, MCPRemovalsFile), []byte(body))
		_, err := m.Shared()
		code(t, err, "MCP_REMOVALS_INVALID")
		if strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
			t.Fatal("corruption error echoed input")
		}
	}
}

func TestMCPDeletionOwnerRejectsRehashedExtraEffects(t *testing.T) {
	m, _ := installFixture(t)
	p := explicitMCPRemoval(t, m)
	before := capture(t, m)
	for _, kind := range []string{"foreign-source", "forged-origin", "missing-marker", "retained-deleted-lock"} {
		t.Run(kind, func(t *testing.T) {
			after := workstore.Clone(p.Plan.After)
			switch kind {
			case "foreign-source":
				if err := m.Store.PutFile(&after, "unrelated.txt", []byte("not part of deletion"), 0644); err != nil {
					t.Fatal(err)
				}
			case "forged-origin":
				records, err := m.mcpRemovals(after)
				if err != nil {
					t.Fatal(err)
				}
				records[0].DefinitionSHA256 = strings.Repeat("a", 64)
				if err = m.putMCPRemovals(&after, map[string]MCPRemoval{records[0].Name: records[0]}); err != nil {
					t.Fatal(err)
				}
			case "missing-marker":
				delete(after.Entries, MCPRemovalsFile)
			case "retained-deleted-lock":
				after.Entries["plugins.lock"] = before.Entries["plugins.lock"]
			}
			forged, err := m.Store.PreviewBound("mcp-edit", before, after, p.Plan.Inputs)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.ApplyMCP(context.Background(), forged.ID, forged.Digest); err == nil {
				t.Fatal("rehashed plan widened MCP deletion", kind)
			}
			unchangedManager(t, m, before)
		})
	}
}

func TestReplayRestoresHistoricalMCPRemovalRecordsInsteadOfRetainingCurrentOnes(t *testing.T) {
	m, _ := installFixture(t)
	m.Copy = workstore.ID()
	saveLocal(t, m, ProfileEdit{Profile: map[string]any{"schema_version": 2, "name": "local", "description": "keep local draft"}})
	historicalRecord := MCPRemoval{Name: "old-service", DefinitionSHA256: strings.Repeat("a", 64), LockSHA256: strings.Repeat("b", 64)}
	write(t, filepath.Join(m.Store.Root, MCPRemovalsFile), jsonBytes(mcpRemovalDocument{Version: 1, Removals: []MCPRemoval{historicalRecord}}))
	bundle, historical, inputs := historicalObservation(t, m)
	currentRecord := MCPRemoval{Name: "later-service", DefinitionSHA256: strings.Repeat("c", 64), LockSHA256: strings.Repeat("d", 64)}
	write(t, filepath.Join(m.Store.Root, MCPRemovalsFile), jsonBytes(mcpRemovalDocument{Version: 1, Removals: []MCPRemoval{currentRecord}}))
	before := capture(t, m)
	draft, err := m.DraftReplay(context.Background(), historical, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if draft.SourceAfter.Entries[MCPRemovalsFile] != historical.Entries[MCPRemovalsFile] {
		t.Fatal("historical replay kept the later deletion intent")
	}
	unchangedManager(t, m, before)
}
