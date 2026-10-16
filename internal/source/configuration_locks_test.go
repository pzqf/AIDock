package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

func requireConfigurationBusy(t *testing.T, err error) {
	t.Helper()
	var e *result.Error
	if !errors.As(err, &e) || e.Code != "SOURCE_BUSY" {
		t.Fatalf("wanted SOURCE_BUSY, got %v", err)
	}
}

func TestConfigurationSetHoldsSharedLibraryAndEveryFirstOverride(t *testing.T) {
	managers := []*Manager{}
	first := manager(t)
	lib := rulesLibrary(t, first)
	for i := 0; i < 5; i++ {
		m := first
		if i > 0 {
			m = manager(t)
		}
		apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
		m.Copy = "copy"
		m.Library, m.ProfileLibrary = lib, true
		managers = append(managers, m)
	}
	if err := WithConfigurationLocks(managers, func() error {
		requireConfigurationBusy(t, lib.Store.Locked(func() error { return nil }))
		for _, m := range managers {
			state, err := m.Profile()
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, Profile: ruleProfile("first", "first local rule")})
			requireConfigurationBusy(t, err)
			s, err := m.localProfileStore()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(s.Root); !os.IsNotExist(err) {
				t.Fatalf("locking created an override: %v", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range managers {
		saveLocal(t, m, ProfileEdit{Profile: ruleProfile("first", "first local rule")})
	}
}

func TestConfigurationSetReleasesPartialAcquisitionAndCallbackFailure(t *testing.T) {
	m := manager(t)
	m.Copy = "copy"
	lib := rulesLibrary(t, m)
	m.Library, m.ProfileLibrary = lib, true
	s, err := m.prepareLocalProfileStore()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Locked(func() error {
		requireConfigurationBusy(t, WithConfigurationLocks([]*Manager{m, m}, func() error {
			t.Fatal("callback ran with a partial lock set")
			return nil
		}))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("consumer failed")
	if err = WithConfigurationLocks([]*Manager{m, m}, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = lib.Store.Locked(func() error { return s.Locked(func() error { return nil }) }); err != nil {
		t.Fatal("configuration lock leaked", err)
	}
	if err = os.WriteFile(filepath.Join(lib.Store.Root, "handwritten"), []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
}
