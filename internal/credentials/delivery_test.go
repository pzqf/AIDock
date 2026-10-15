package credentials

import (
	"context"
	"errors"
	"github.com/pzqf/AIDock/internal/secref"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deliveryBinding() Delivery {
	return Delivery{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
}
func deliveryRefs() []secref.Reference {
	return []secref.Reference{{Variable: "API_TOKEN", Name: "alpha"}, {Variable: "SECOND_TOKEN", Name: "alpha"}}
}
func TestVaultRequestedOnlyHandoffAuditWipeAndNoDiskValues(t *testing.T) {
	m := manager(t)
	entry := addSecret(t, m, "alpha")
	addSecret(t, m, "unused")
	var retained []string
	called := 0
	err := m.WithEnvironment(context.Background(), deliveryBinding(), deliveryRefs(), func(env []string) error {
		called++
		retained = env
		if len(env) != 2 || env[0] != "API_TOKEN=FAKE_VAULT_PRIVATE_SENTINEL" {
			t.Fatal("wrong env")
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatal(err, called)
	}
	for _, s := range retained {
		if s != "" {
			t.Fatal("handoff slice retained value")
		}
	}
	state, err := m.State(context.Background())
	if err != nil || len(state.History) != 5 || len(state.Pending) != 0 {
		t.Fatal(state, err)
	}
	a := state.History[4]
	if a.Action != "inject" || a.Name != "alpha" || a.After != entry.Revision || a.Outcome != "AUTHORIZED" || a.Delivery == nil {
		t.Fatal(a)
	}
	if err = m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(m.local, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil || strings.Contains(string(b), "PRIVATE_SENTINEL") {
				t.Fatal("secret on disk", p)
			}
		}
		return nil
	})
}
func TestVaultDeliveryFailsClosedAndRedactsConsumer(t *testing.T) {
	for _, kind := range []string{"missing", "pending", "corrupt-audit", "bad-binding", "duplicate", "reserved", "consumer", "panic", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			addSecret(t, m, "alpha")
			binding := deliveryBinding()
			refs := deliveryRefs()
			ctx := context.Background()
			want := ""
			calls := 0
			switch kind {
			case "missing":
				refs[1].Name = "missing"
				want = "CREDENTIAL_MISSING"
			case "pending":
				m.hook = func(string) error { return errors.New("crash") }
				_, _ = m.Change(ctx, Edit{Action: "add", Name: "other", Value: "test", Confirm: true})
				want = "CREDENTIAL_RECOVERY_REQUIRED"
			case "corrupt-audit":
				_ = os.WriteFile(filepath.Join(m.directory(), "00000001.json"), []byte("broken"), 0600)
				want = "CREDENTIAL_AUDIT_INVALID"
			case "bad-binding":
				binding.InputSHA256 = ""
				want = "CREDENTIAL_DELIVERY_INVALID"
			case "duplicate":
				refs[1].Variable = refs[0].Variable
				want = "CREDENTIAL_DELIVERY_INVALID"
			case "reserved":
				refs[0].Variable = "PATH"
				want = "CREDENTIAL_DELIVERY_INVALID"
			case "consumer", "panic":
				want = "CREDENTIAL_CONSUMER_FAILED"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := m.WithEnvironment(ctx, binding, refs, func([]string) error {
				calls++
				if kind == "panic" {
					panic("FAKE_VAULT_PRIVATE_SENTINEL")
				}
				return errors.New("FAKE_VAULT_PRIVATE_SENTINEL")
			})
			if want != "" {
				requireCode(t, err, want)
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("leaked error", err)
			}
			if kind != "consumer" && kind != "panic" && calls != 0 {
				t.Fatal("delivered despite failure")
			}
		})
	}
}
func TestVaultRotationWaitsForDelivery(t *testing.T) {
	m := manager(t)
	entry := addSecret(t, m, "alpha")
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.WithEnvironment(context.Background(), deliveryBinding(), deliveryRefs(), func([]string) error { close(inside); <-release; return nil })
	}()
	<-inside
	rotated := make(chan error, 1)
	go func() {
		_, err := m.Change(context.Background(), Edit{Action: "rotate", Name: "alpha", ExpectedRevision: entry.Revision, Value: "new-test-value", Confirm: true})
		rotated <- err
	}()
	select {
	case <-rotated:
		t.Fatal("rotation interleaved with handoff")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-rotated; err != nil {
		t.Fatal(err)
	}
}
