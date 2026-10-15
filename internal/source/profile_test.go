package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestProfileBatchIsOneRecoverableSourceTransaction(t *testing.T) {
	m := manager(t)
	a := apply(t, m, Request{Action: "create", Name: "guide-a", Description: "a"})
	b := apply(t, m, Request{Action: "create", Name: "guide-b", Description: "b"})
	old, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	old.Profile["name"] = "团队开发"
	old.Profile["description"] = "两项一起启用"
	setEnabled(old.Profile, a.SkillID, true)
	setEnabled(old.Profile, b.SkillID, true)
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: old.SHA256, Profile: old.Profile})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range p.Plan.Changes {
		if change.Path != "profile.yaml" {
			t.Fatal("profile edit changed another path", change)
		}
	}
	now, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("preview wrote source")
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil || len(switches(state.Profile)["enable"].([]any)) != 2 || state.Report == nil {
		t.Fatal(state, err)
	}
	rolled := apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	now, _ = m.Store.Capture(m.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("rollback lost original profile bytes")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: rolled.Plan.ID})
	state, err = m.Profile()
	if err != nil || state.Profile["name"] != "团队开发" {
		t.Fatal(state, err)
	}
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: old.SHA256, Profile: old.Profile})
	code(t, err, "PLAN_STALE")
}
func TestProfileInvalidBatchCannotPartiallyEnable(t *testing.T) {
	m := manager(t)
	a := apply(t, m, Request{Action: "create", Name: "guide", Description: "a"})
	old, _ := m.Profile()
	setEnabled(old.Profile, a.SkillID, true)
	switches(old.Profile)["enable"] = append(switches(old.Profile)["enable"].([]any), "99999999-9999-4999-8999-999999999999")
	_, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: old.SHA256, Profile: old.Profile})
	code(t, err, "PROFILE_UNKNOWN_ID")
	state, _ := m.Profile()
	if len(switches(state.Profile)["enable"].([]any)) != 0 {
		t.Fatal("invalid batch partially saved")
	}
	state.Profile["rules"] = []any{"修改前先核对范围"}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Preflight.ProfileChecks) != 1 || p.Preflight.ProfileChecks[0].Code != "PROFILE_RULES_PENDING" {
		t.Fatal(p.Preflight)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func TestProfileFailureRestoresAllBytesAndRejectsExternalModification(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "a"})
	state, _ := m.Profile()
	state.Profile["name"] = "candidate"
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	m.Store.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			return errors.New("injected profile failure")
		}
		return nil
	}
	_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	if err == nil {
		t.Fatal("failure injection missed")
	}
	m.Store.Hook = nil
	if _, err = m.Recover(); err != nil {
		t.Fatal(err)
	}
	state, _ = m.Profile()
	if state.Profile["name"] == "candidate" {
		t.Fatal("failed profile remained")
	}
	p, err = m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.Root, "profile.yaml")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, append(bytes, '\n'))
	_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
}
