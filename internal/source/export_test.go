package source

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestExportOnlySavedEnabledPinnedCompletePackages(t *testing.T) {
	m := manager(t)
	_, err := m.Export()
	code(t, err, "SOURCE_NOT_INITIALIZED")
	local := apply(t, m, Request{Action: "create", Name: "local-guide", Description: "example"})
	apply(t, m, Request{Action: "create", Name: "disabled-guide", Description: "disabled"})
	l := library(t)
	global := apply(t, l, Request{Action: "create", Name: "global-guide", Description: "example"})
	attach(t, m, l)
	g := selectedSkill(t, l, global.SkillID)
	apply(t, m, Request{Action: "inherit", ID: g.ID, Revision: g.Revision})
	reg, _ := externalFixture(t, m)
	external := apply(t, m, adoption(t, m))
	for _, id := range []string{local.SkillID, g.ID, external.SkillID} {
		apply(t, m, Request{Action: "enable", ID: id})
	}
	before := capture(t, m)
	exported, err := m.Export()
	if err != nil {
		t.Fatal(err)
	}
	if exported.Snapshot != workstore.Digest(before) || len(exported.Packages) != 3 {
		t.Fatal(exported)
	}
	layers := map[string]ExportPackage{}
	for _, p := range exported.Packages {
		layers[p.Layer] = p
		if p.Tree.Entries["SKILL.md"].Hash == "" {
			t.Fatal("missing document")
		}
	}
	if len(layers) != 3 || !layers["external"].Tree.Entries["empty"].Dir || layers["external"].Tree.Entries["assets/data.bin"].Hash != workstore.Hash([]byte{0, 1, 255}) {
		t.Fatal("incomplete effective export")
	}
	if !reflect.DeepEqual(before, capture(t, m)) {
		t.Fatal("source changed")
	}
	// Neither disappearance nor a new upstream candidate changes a saved pin.
	apply(t, l, Request{Action: "edit", ID: g.ID, Files: []Patch{{Path: "SKILL.md", Content: string(body("global-guide")) + "upstream"}}})
	if err := os.Rename(reg.Root, reg.Root+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.BridgeFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(l.Store.Root, l.Store.Root+"-offline"); err != nil {
		t.Fatal(err)
	}
	again, err := m.Export()
	if err != nil || !reflect.DeepEqual(exported.Packages, again.Packages) {
		t.Fatal("lost pinned offline bytes", err)
	}
	if len(again.Authorities) != 1 || again.Authorities[0] != reg.Root {
		t.Fatal("lost historical authority")
	}
	if _, err := os.Stat(filepath.Join(m.Store.Root, "skills", external.SkillID)); !os.IsNotExist(err) {
		t.Fatal("external editable copy")
	}
}
