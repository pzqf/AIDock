package config

import (
	"strings"
	"testing"
)

func TestMigrationUniqueNamesAndUnresolvedReferences(t *testing.T) {
	old := []byte("schema_version: 1\nname: example\nskills:\n  enable: [guide]\nrules: [keep]\n")
	binding := SkillBinding{ID: "11111111-1111-4111-8111-111111111111", Revision: "sha256:" + strings.Repeat("a", 64)}
	data, e := MigrateProfile(old, map[string][]SkillBinding{"guide": {binding}})
	if e != nil || !strings.Contains(string(data), "keep") {
		t.Fatal(string(data), e)
	}
	for _, names := range []map[string][]SkillBinding{{}, {"guide": {binding, binding}}, {"guide": {{ID: "not-uuid", Revision: binding.Revision}}}} {
		if _, e = MigrateProfile(old, names); e == nil {
			t.Fatal("ambiguous/invalid migration accepted")
		}
	}
}
