package source

import (
	"path/filepath"
	"testing"
)

func TestStateDescriptionsUseSavedManifest(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "项目接口约定", Document: "instructions"})
	s := current(t, m)
	if s.Descriptions[p.SkillID] != "项目接口约定" {
		t.Fatal(s.Descriptions)
	}
	// A changed source must not be represented as the saved description.
	write(t, filepath.Join(m.Store.Root, "skills", p.SkillID, "SKILL.md"), body("guide"))
	s = current(t, m)
	if _, ok := s.Descriptions[p.SkillID]; ok {
		t.Fatal("drifting source presented as saved metadata", s.Descriptions)
	}
}

func TestStateDescriptionsUsePinnedCache(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "guide", Description: "固定版本说明", Document: "instructions"})
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: p.SkillID, Revision: selectedSkill(t, l, p.SkillID).Revision})
	apply(t, l, Request{Action: "edit", ID: p.SkillID, Files: []Patch{{Path: "SKILL.md", Content: string(body("guide"))}}})
	if got := current(t, m).Descriptions[p.SkillID]; got != "固定版本说明" {
		t.Fatal("candidate update replaced pinned description", got)
	}
}
