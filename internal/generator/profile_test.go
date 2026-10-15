package generator

import (
	"github.com/pzqf/AIDock/internal/source"
	"testing"
)

func TestSkillOnlyGeneratorRejectsUnrepresentedProfileRules(t *testing.T) {
	m, adapter := fixture(t)
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"never silently drop this"}
	p, err := m.Source.ProfilePlan(ctx, source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.Inspect(ctx, adapter)
	code(t, err, "GENERATOR_PROFILE_UNSUPPORTED")
}
