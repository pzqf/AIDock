package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func auditChain(t *testing.T, chains []auditlog.Chain, category string) auditlog.Chain {
	t.Helper()
	for _, c := range chains {
		if c.Category == category {
			return c
		}
	}
	t.Fatal("missing chain", category)
	return auditlog.Chain{}
}

func TestAuditQueryRedactsReasonsKeepsRevokedHistoryAndReadsDamagedSource(t *testing.T) {
	m, req := installFixture(t)
	edit := allowInstall(req.Name)
	edit.RiskReason = "PRIVATE_INSTALL_REASON"
	installEdit(t, m, edit)
	grant := grantPermission(req.Name)
	grant.Purpose = "PRIVATE_PERMISSION_PURPOSE"
	permissionApply(t, m, permissionPlan(t, m, grant))
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), []byte("schema_version: [PRIVATE_BROKEN_SOURCE"))
	before, _ := m.Store.Capture(m.Store.Root)
	chains := m.AuditChains(context.Background())
	i, p := auditChain(t, chains, "install"), auditChain(t, chains, "permission")
	if len(chains) != 5 || i.Status != "PASS" || *i.Count != 1 || len(i.Events) != 1 || i.Events[0].Code != "RISK_ACCEPTED" || p.Status != "PASS" || *p.Count != 2 || p.Events[0].Action != "grant" || p.Events[1].Action != "revoke" || p.Events[0].Actor == "" {
		t.Fatal(chains)
	}
	page := auditlog.Paginate(chains, auditlog.Query{Page: 1})
	b, _ := json.Marshal(page)
	for _, private := range []string{"PRIVATE_INSTALL_REASON", "PRIVATE_PERMISSION_PURPOSE", "PRIVATE_BROKEN_SOURCE", "permissions", "purpose", "risk_reason", "manifest_document"} {
		if strings.Contains(string(b), private) {
			t.Fatal("private field escaped into audit view", private)
		}
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("audit changed damaged source")
	}
	other, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	other.Copy = "other-copy"
	foreign := other.AuditChains(context.Background())
	if *auditChain(t, foreign, "permission").Count != 0 || *auditChain(t, foreign, "install").Count != 1 {
		t.Fatal("copy audit leaked or source scope lost", foreign)
	}
}

func TestAuditQueryDamagedChainRetainsOtherCategoriesAndNeverRepairs(t *testing.T) {
	m, name := permissionFixture(t)
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	p, _ := m.permissionManager()
	dir := filepath.Join(p.Store.Root, "audit")
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	file := filepath.Join(dir, files[0].Name())
	if err := os.WriteFile(file, []byte("PRIVATE_CORRUPT_AUDIT"), 0600); err != nil {
		t.Fatal(err)
	}
	chains := m.AuditChains(context.Background())
	broken := auditChain(t, chains, "permission")
	if broken.Status != "FAIL" || broken.Count != nil || len(broken.Events) != 0 || broken.Head != "" || auditChain(t, chains, "install").Status != "PASS" {
		t.Fatal(chains)
	}
	if b, _ := os.ReadFile(file); string(b) != "PRIVATE_CORRUPT_AUDIT" {
		t.Fatal("audit repaired or replaced damage")
	}
}
