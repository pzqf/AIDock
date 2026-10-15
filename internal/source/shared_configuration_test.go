package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestSharedConfigurationProjectionAndIndependentCandidate(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["description"] = "target description"
	state.Profile["config_overrides"] = map[string]any{"private": map[string]any{"path": "machine-only", "token": "do-not-share"}}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Store.Root, "rules", "unowned.md"), []byte("preserve unowned"))
	before, _ := m.Store.Capture(m.Store.Root)
	c, err := m.Shared()
	if err != nil || len(c.Configurations) != 2 || len(c.ConfigurationNotes) != 2 {
		t.Fatal(c.ConfigurationNotes, err)
	}
	if strings.Contains(string(jsonBytes(c.Configurations)), "do-not-share") {
		t.Fatal("unregistered config values leaked into portable entries")
	}
	selected := c.Configurations[0]
	delete(selected.Profile, "description")
	selected.Profile["name"] = "adopted"
	selected.Digest = SharedConfigurationDigest(selected)
	rules := c.Configurations[1]
	rules.Profile["rules"] = []any{"shared inline"}
	rules.Rules = []RuleFile{{ID: workstore.ID(), Name: "rule", Enabled: true, Body: "\xef\xbb\xbf# exact\r\ncontent\r\n"}}
	rules.Digest = SharedConfigurationDigest(rules)
	after, _, err := m.DraftSharedConfiguration(before, nil, []SharedConfiguration{selected, rules}, m.Store)
	if err != nil {
		t.Fatal(err)
	}
	_, v, err := m.load(&after)
	if err != nil || v["name"] != "adopted" || v["description"] != nil || string(jsonBytes(v["skills"])) != string(jsonBytes(state.Profile["skills"])) || string(jsonBytes(v["config_overrides"])) != string(jsonBytes(state.Profile["config_overrides"])) {
		t.Fatal(v, err)
	}
	for _, path := range []string{"skills.yaml", "rules/unowned.md"} {
		if after.Entries[path] != before.Entries[path] {
			t.Fatal("configuration decision changed unrelated bytes", path)
		}
	}
	body, err := m.Store.Data(after.Entries[ruleFilePath(rules.Rules[0].ID)])
	if err != nil || string(body) != rules.Rules[0].Body {
		t.Fatal("rule bytes changed", err)
	}
	now, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("candidate modified original root")
	}
}

func TestSharedConfigurationRejectsOwnershipCollisionAndForgedFields(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	c, err := m.Shared()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"skills", "config_overrides", "config_remove", "rules", "schema_version", "unknown"} {
		t.Run(key, func(t *testing.T) {
			v := c.Configurations[0]
			v.Profile = cloneProfile(v.Profile)
			v.Profile[key] = map[string]any{}
			v.Digest = SharedConfigurationDigest(v)
			code(t, ValidateSharedConfiguration(v), "REPLICA_CONFIGURATION_INVALID")
		})
	}
	r := c.Configurations[1]
	r.Rules = []RuleFile{{ID: workstore.ID(), Name: "rule", Enabled: true, Body: "source rule"}}
	r.Digest = SharedConfigurationDigest(r)
	path := filepath.Join(m.Store.Root, ruleFilePath(r.Rules[0].ID))
	write(t, path, []byte("unowned local file"))
	before, _ := m.Store.Capture(m.Store.Root)
	_, _, err = m.DraftSharedConfiguration(before, nil, []SharedConfiguration{r}, m.Store)
	code(t, err, "RULE_FILE_UNOWNED")
	got, _ := os.ReadFile(path)
	if string(got) != "unowned local file" {
		t.Fatal("unowned rule overwritten")
	}
	r.Rules = append(r.Rules, r.Rules[0])
	r.Digest = SharedConfigurationDigest(r)
	code(t, ValidateSharedConfiguration(r), "REPLICA_CONFIGURATION_INVALID")
}
