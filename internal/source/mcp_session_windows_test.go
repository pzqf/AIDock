package source

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/pzqf/AIDock/internal/credentials"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSessionInjectsOnlyAuthorizedCredentialAndDiscardsUpstreamMetadata(t *testing.T) {
	ctx := context.Background()
	m, req := sessionFixture(t, true)
	e, err := m.CheckMCPSession(ctx, req)
	if err != nil || e.Code != "MCP_SESSION_START_FAILED" {
		t.Fatal(e, err)
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	secret := "DYNAMIC_SESSION_TEST_" + hex.EncodeToString(random)
	vault, err := credentials.New(m.Store.Local)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := vault.Change(ctx, credentials.Edit{Action: "add", Name: "session_key", Value: secret, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := vault.Change(ctx, credentials.Edit{Action: "delete", Name: entry.Name, ExpectedRevision: entry.Revision, ReferencesReviewed: true, Confirm: true}); err != nil {
			t.Error(err)
		}
	}()
	e, err = m.CheckMCPSession(ctx, req)
	if err != nil || e.Code != "MCP_SESSION_MATCH" {
		t.Fatal(e, err)
	}
	state, err := vault.State(ctx)
	if err != nil || state.History[len(state.History)-1].Action != "inject" || state.History[len(state.History)-1].After != entry.Revision {
		t.Fatal(state, err)
	}
	if err = filepath.WalkDir(m.Store.Local, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret persisted: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
