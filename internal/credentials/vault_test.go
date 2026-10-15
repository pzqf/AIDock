package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

type fakeValue struct {
	entry Entry
	value []byte
}
type fakeBackend struct {
	mu     sync.Mutex
	values map[string]fakeValue
	broken bool
}

func (b *fakeBackend) available() bool { return true }
func (b *fakeBackend) locked(ctx context.Context, fn func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (b *fakeBackend) list() ([]Entry, error) {
	out := []Entry{}
	for _, v := range b.values {
		out = append(out, v.entry)
	}
	return out, nil
}
func (b *fakeBackend) read(n string) (Entry, []byte, error) {
	v, ok := b.values[n]
	if !ok {
		return Entry{}, nil, errMissing
	}
	return v.entry, append([]byte(nil), v.value...), nil
}
func (b *fakeBackend) write(e Entry, v []byte) error {
	if b.broken {
		return errors.New("fake OS failure")
	}
	b.values[e.Name] = fakeValue{e, append([]byte(nil), v...)}
	return nil
}
func (b *fakeBackend) remove(n string) error { delete(b.values, n); return nil }
func manager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(filepath.Clean(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	m.store = &fakeBackend{values: map[string]fakeValue{}}
	return m
}
func addSecret(t *testing.T, m *Manager, name string) Entry {
	t.Helper()
	e, err := m.Change(context.Background(), Edit{Action: "add", Name: name, Value: "FAKE_VAULT_PRIVATE_SENTINEL", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *result.Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("expected %s; got %v", code, err)
	}
}
func TestVaultLifecycleRevisionsAndNoDiskValues(t *testing.T) {
	m := manager(t)
	ctx := context.Background()
	e := addSecret(t, m, "test_key")
	_, err := m.Change(ctx, Edit{Action: "add", Name: e.Name, Value: "duplicate", Confirm: true})
	requireCode(t, err, "CREDENTIAL_EXISTS")
	_, err = m.Change(ctx, Edit{Action: "rotate", Name: e.Name, ExpectedRevision: strings.Repeat("0", 32), Value: "new", Confirm: true})
	requireCode(t, err, "CREDENTIAL_STALE")
	next, err := m.Change(ctx, Edit{Action: "rotate", Name: e.Name, ExpectedRevision: e.Revision, Value: "FAKE_NEW_PRIVATE_SENTINEL", Confirm: true})
	if err != nil || next.Revision == e.Revision {
		t.Fatal(next, err)
	}
	state, err := m.State(ctx)
	if err != nil || len(state.Entries) != 1 || len(state.History) != 4 || len(state.Pending) != 0 {
		t.Fatal(state, err)
	}
	_, err = m.Change(ctx, Edit{Action: "delete", Name: e.Name, ExpectedRevision: next.Revision, Confirm: true})
	requireCode(t, err, "CREDENTIAL_REFERENCES_REVIEW_REQUIRED")
	_, err = m.Change(ctx, Edit{Action: "delete", Name: e.Name, ExpectedRevision: next.Revision, Confirm: true, ReferencesReviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	state, err = m.State(ctx)
	if err != nil || len(state.Entries) != 0 || len(state.History) != 6 {
		t.Fatal(state, err)
	}
	serialized, _ := json.Marshal(state)
	if strings.Contains(string(serialized), "PRIVATE_SENTINEL") {
		t.Fatal("state leaked value")
	}
	filepath.WalkDir(m.local, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(b), "PRIVATE_SENTINEL") {
				t.Fatal("value on disk", p)
			}
		}
		return nil
	})
}
func TestVaultInterruptedBeforeAndAfterOSWrite(t *testing.T) {
	for _, point := range []string{"intent", "vault"} {
		t.Run(point, func(t *testing.T) {
			m := manager(t)
			m.hook = func(p string) error {
				if p == point {
					return errors.New("injected crash")
				}
				return nil
			}
			_, err := m.Change(context.Background(), Edit{Action: "add", Name: "test_key", Value: "fake", Confirm: true})
			if err == nil {
				t.Fatal("missing crash")
			}
			state, err := m.State(context.Background())
			if err != nil || len(state.Pending) != 1 {
				t.Fatal(state, err)
			}
			m.hook = nil
			_, err = m.Change(context.Background(), Edit{Action: "add", Name: "other", Value: "fake", Confirm: true})
			requireCode(t, err, "CREDENTIAL_RECOVERY_REQUIRED")
			if err = m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			state, err = m.State(context.Background())
			if err != nil || len(state.Pending) != 0 {
				t.Fatal(state, err)
			}
			want := "APPLIED"
			if point == "intent" {
				want = "NOT_APPLIED"
			}
			if state.History[1].Outcome != want {
				t.Fatal(state.History)
			}
		})
	}
}
func TestVaultDeleteInterruptionAndExternalConflict(t *testing.T) {
	for _, action := range []string{"delete", "rotate"} {
		t.Run(action, func(t *testing.T) {
			m := manager(t)
			entry := addSecret(t, m, "test_key")
			m.hook = func(p string) error {
				if p == "vault" {
					return errors.New("crash")
				}
				return nil
			}
			_, err := m.Change(context.Background(), Edit{Action: action, Name: entry.Name, Value: map[string]string{"rotate": "fake"}[action], ExpectedRevision: entry.Revision, Confirm: true, ReferencesReviewed: true})
			if err == nil {
				t.Fatal("no crash")
			}
			if action == "rotate" {
				b := m.store.(*fakeBackend)
				v := b.values[entry.Name]
				v.entry.Revision = randomID()
				b.values[entry.Name] = v
				requireCode(t, m.Reconcile(context.Background()), "CREDENTIAL_RECOVERY_CONFLICT")
			} else if err = m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestVaultInvalidInputDoesNotTouchOSOrJournal(t *testing.T) {
	for _, req := range []Edit{{Action: "add", Name: "bad/name", Value: "fake", Confirm: true}, {Action: "add", Name: "key", Value: "fake"}, {Action: "add", Name: "key", Value: strings.Repeat("x", MaxValue+1), Confirm: true}, {Action: "add", Name: "key", Value: "\x00", Confirm: true}, {Action: "rotate", Name: "key", Value: "fake", Confirm: true}, {Action: "add", Name: "key", Value: "fake", ExpectedRevision: randomID(), Confirm: true}} {
		m := manager(t)
		_, err := m.Change(context.Background(), req)
		if err == nil {
			t.Fatal(req.Action, "accepted")
		}
		if _, e := os.Stat(m.directory()); !os.IsNotExist(e) {
			t.Fatal("wrote journal")
		}
		if len(m.store.(*fakeBackend).values) != 0 {
			t.Fatal("wrote OS")
		}
	}
}
func TestStrictCredentialRequestMasksInputAndRejectsAliases(t *testing.T) {
	for _, raw := range []string{`{"action":"add","value":"PRIVATE_SENTINEL","value":"fake"}`, `{"Action":"add","value":"PRIVATE_SENTINEL"}`, `{"value":null}`, `{"value":{"PRIVATE_SENTINEL":true}}`, `{"confirm":false,"confirm":true}`, `{} {}`, `{} garbage`, `[]`, `{"value":"PRIVATE_SENTINEL","extra":1}`} {
		_, err := ParseEdit([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
			t.Fatal("accepted or leaked", err)
		}
	}
	for _, raw := range []string{`{}`, " {\"action\":\"add\",\"name\":\"key\",\"value\":\"fake\",\"confirm\":true} \r\n"} {
		if _, err := ParseEdit([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAuditTamperBlocksNewChanges(t *testing.T) {
	m := manager(t)
	addSecret(t, m, "key")
	p := filepath.Join(m.directory(), "00000001.json")
	if err := os.WriteFile(p, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := m.Change(context.Background(), Edit{Action: "add", Name: "other", Value: "fake", Confirm: true})
	requireCode(t, err, "CREDENTIAL_AUDIT_INVALID")
}
func TestConcurrentAddSerializesAndCancelledOperationHasNoIntent(t *testing.T) {
	m := manager(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Change(context.Background(), Edit{Action: "add", Name: "key", Value: "fake", Confirm: true})
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatal(success)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Change(ctx, Edit{Action: "add", Name: "other", Value: "fake", Confirm: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, _ := m.State(context.Background())
	if len(s.History) != 2 {
		t.Fatal(s.History)
	}
}
