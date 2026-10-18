package source

import (
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// WithConfigurationLocks holds all explicitly bound library and machine
// profile stores through fn. Shared libraries are locked once, even when
// several copies refer to them. Project source locks belong to the caller.
// fn must not call a method that locks a configuration store again.
func WithConfigurationLocks(managers []*Manager, fn func() error) error {
	return WithConfigurationTransactionLocks(managers, nil, fn)
}

// The owning common coordinator locks the listed machine-profile roots. Hold
// every library and all OTHER machine profiles outside it without reentry.
// No omitted root is inferred; each must belong to one supplied manager.
func WithConfigurationTransactionLocks(managers []*Manager, coordinatedLocal []string, fn func() error) error {
	stores := map[string]*workstore.Store{}
	skipped := map[string]bool{}
	for _, root := range coordinatedLocal {
		key := strings.ToLower(root)
		if skipped[key] {
			return result.Fail("INVALID_SOURCE_BINDING", "共同事务的本机配置根重复", 2)
		}
		skipped[key] = true
	}
	known := map[string]bool{}
	for _, m := range managers {
		if m == nil || m.Store == nil {
			return result.Fail("INVALID_SOURCE_BINDING", "配置集合需要明确的源管理器", 2)
		}
		if m.Library != nil {
			stores[strings.ToLower(m.Library.Store.Root)] = m.Library.Store
		}
		if m.Owner != "global" && m.Copy != "" {
			// The lock lives beside the override root. Its parent must exist
			// even before the first override is saved, otherwise an export can
			// race that first save while believing there are no local layers.
			s, err := m.prepareLocalProfileStore()
			if err != nil {
				return err
			}
			key := strings.ToLower(s.Root)
			known[key] = true
			if !skipped[key] {
				stores[key] = s
			}
		}
	}
	for key := range skipped {
		if !known[key] {
			return result.Fail("INVALID_SOURCE_BINDING", "共同事务省略了未绑定的本机配置根", 2)
		}
	}
	keys := make([]string, 0, len(stores))
	for key := range stores {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var acquire func(int) error
	acquire = func(i int) error {
		if i == len(keys) {
			return fn()
		}
		return stores[keys[i]].Locked(func() error { return acquire(i + 1) })
	}
	return acquire(0)
}
