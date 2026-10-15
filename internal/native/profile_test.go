package native

import (
	"context"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestUncompiledProfileRulesBlockSkillOnlyInstallation(t *testing.T) {
	m, _ := fixture(t)
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"Never silently omit the project rules"}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	preview, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Blocked || !has(preview, "PROFILE_NATIVE_UNSUPPORTED") {
		t.Fatal("uncompiled rules silently dropped", preview)
	}
}
