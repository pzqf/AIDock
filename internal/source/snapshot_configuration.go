package source

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// SnapshotConfiguration retains original local input separately from portable
// project source. It is private recovery material, never a replica/shared export.
// Portable is the resolved default/extends/project configuration before Local.
type SnapshotConfiguration struct {
	Version           int             `json:"schema_version"`
	SourceDigest      string          `json:"source_digest"`
	Layers            ProfileLayers   `json:"layers"`
	LocalTree         workstore.Tree  `json:"local_tree"`
	Portable          profile.Report  `json:"portable"`
	SkillLibraryRoot  string          `json:"skill_library_root,omitempty"`
	ProfileLibrary    bool            `json:"profile_library"`
	ReferencePackages []ExportPackage `json:"reference_packages,omitempty"`
	Digest            string          `json:"digest"`
}

func LocalProfileRoot(local, project, scope, copy string) string {
	return filepath.Join(local, "profile-overrides", workstore.Hash(jsonBytes([]string{project, scope, copy})))
}

// Validation is independent of live source/library files. Missing current
// dependencies must not prevent opening the immutable recovery observation.
func ValidateSnapshotConfiguration(s SnapshotConfiguration, bundle Export, local string) error {
	bad := func() error {
		return result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "快照配置层、本机原件或摘要不一致", 5)
	}
	if (s.Version != 1 && s.Version != 2) || s.Version == 1 && len(s.ReferencePackages) != 0 || s.Digest != SnapshotConfigurationDigest(s) || s.SourceDigest != bundle.Snapshot || s.Layers.Digest != bundle.ConfigurationDigest || !s.Layers.LocalAvailable || s.Layers.LocalRoot != LocalProfileRoot(local, bundle.Owner, bundle.Scope, bundle.Copy) || workstore.Digest(s.LocalTree) != s.Layers.LocalSHA256 || s.Layers.Recovery || len(s.Layers.History) != 0 {
		return bad()
	}
	if err := validateSnapshotReferences(s, bundle); err != nil {
		return err
	}
	if !s.LocalTree.Exists && (len(s.LocalTree.Entries) != 0 || s.Layers.Local != nil) {
		return bad()
	}
	if s.LocalTree.Exists {
		if len(s.LocalTree.Entries) < 1 || len(s.LocalTree.Entries) > 2 {
			return bad()
		}
		if e, ok := s.LocalTree.Entries["binding.json"]; !ok || e.Dir {
			return bad()
		}
		for p, e := range s.LocalTree.Entries {
			if e.Dir || (p != "binding.json" && p != "profile.yaml") {
				return bad()
			}
		}
		_, hasProfile := s.LocalTree.Entries["profile.yaml"]
		if hasProfile != (s.Layers.Local != nil) {
			return bad()
		}
	}
	for _, root := range []string{s.SkillLibraryRoot, s.Layers.LibraryRoot} {
		if root != "" && (!filepath.IsAbs(root) || filepath.Clean(root) != root) {
			return bad()
		}
	}
	if s.ProfileLibrary != (s.Layers.LibraryRoot != "") || s.ProfileLibrary && s.SkillLibraryRoot != s.Layers.LibraryRoot {
		return bad()
	}
	for _, value := range append([]map[string]any{s.Layers.Defaults, s.Layers.Local, s.Portable.Effective}, s.Layers.Profiles...) {
		if value != nil {
			if _, err := config.Validate("profile", jsonBytes(value)); err != nil {
				return bad()
			}
		}
	}
	portable := s.Portable
	portable.Digest = ""
	encoded, _ := json.Marshal(portable)
	if s.Portable.Version != 1 || s.Portable.Digest != workstore.Hash(encoded) {
		return bad()
	}
	if bundle.ProfileReport == nil || s.Layers.Local != nil && s.Layers.Local["extends"] != nil && s.Layers.Local["extends"] != "" {
		return bad()
	}
	catalog := profile.Catalog{Skills: map[string][]string{}}
	for _, value := range []map[string]any{s.Portable.Effective, bundle.ProfileReport.Effective, s.Layers.Local} {
		if value == nil {
			continue
		}
		section, _ := value["skills"].(map[string]any)
		pins, _ := section["pins"].(map[string]any)
		for id, raw := range pins {
			revision, ok := raw.(string)
			if !ok {
				return bad()
			}
			catalog.Skills[id] = append(catalog.Skills[id], revision)
		}
		for _, kind := range []string{"plugins", "mcp"} {
			section, _ := value[kind].(map[string]any)
			for _, action := range []string{"enable", "disable"} {
				for _, raw := range array(section[action]) {
					id, ok := raw.(string)
					if !ok {
						return bad()
					}
					if kind == "plugins" {
						catalog.Plugins = append(catalog.Plugins, id)
					} else {
						catalog.MCP = append(catalog.MCP, id)
					}
				}
			}
		}
	}
	combined, err := profile.Resolve(profile.Input{Project: s.Portable.Effective, Local: s.Layers.Local, Catalog: catalog})
	if err != nil || workstore.Hash(jsonBytes(combined.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) {
		return bad()
	}
	return nil
}

func ValidateSnapshotConfigurationBytes(s SnapshotConfiguration, bundle Export, data func(workstore.Entry) ([]byte, error)) error {
	if err := validateSnapshotReferenceBytes(s.ReferencePackages, data); err != nil {
		return err
	}
	bad := func() error {
		return result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "配置层原件与归档观察不一致", 5)
	}
	if !s.LocalTree.Exists {
		return nil
	}
	body, err := data(s.LocalTree.Entries["binding.json"])
	if err != nil {
		return err
	}
	parsed, err := config.Parse(body)
	if err != nil {
		return bad()
	}
	var binding profileBinding
	decoder := json.NewDecoder(bytes.NewReader(jsonBytes(parsed)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || decoder.Decode(new(any)) != io.EOF || binding.Version != 1 || binding.Project != bundle.Owner || binding.Scope != bundle.Scope || binding.Copy != bundle.Copy || binding.SourceRoot != bundle.Root {
		return bad()
	}
	if binding.LibraryRoot != "" && (!filepath.IsAbs(binding.LibraryRoot) || filepath.Clean(binding.LibraryRoot) != binding.LibraryRoot) {
		return bad()
	}
	if e, ok := s.LocalTree.Entries["profile.yaml"]; ok {
		body, err = data(e)
		if err != nil {
			return err
		}
		value, err := config.Validate("profile", body)
		if err != nil || workstore.Hash(jsonBytes(value)) != workstore.Hash(jsonBytes(s.Layers.Local)) {
			return bad()
		}
	}
	return nil
}

func SnapshotConfigurationDigest(s SnapshotConfiguration) string {
	s.Digest = ""
	return workstore.Hash(jsonBytes(s))
}

// WithSnapshotInputs observes project and configuration roots under the same
// locks used by application. Local bytes are copied only to immutable Local
// blobs, not to the portable source root. The consumer must not reenter managers.
func (m *Manager) WithSnapshotInputs(use func(Export, workstore.Tree, SnapshotConfiguration) error) error {
	if use == nil {
		return result.Fail("INVALID_INPUT", "快照观察需要明确消费者", 2)
	}
	return m.Store.Locked(func() error {
		return m.lockProfileLayers(func() error {
			bundle, err := m.exportLocked()
			if err != nil {
				return err
			}
			t, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(t) != bundle.Snapshot {
				return result.Fail("PLAN_STALE", "快照观察期间项目规范源变化", 3)
			}
			layers, err := m.profileLayers()
			if err != nil {
				return err
			}
			if layers.Digest != bundle.ConfigurationDigest {
				return result.Fail("PLAN_STALE", "快照观察期间配置输入变化", 3)
			}
			s, err := m.localProfileStore()
			if err != nil {
				return err
			}
			local, err := s.Capture(s.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(local) != layers.LocalSHA256 {
				return result.Fail("PLAN_STALE", "本机原件与配置观察不一致", 3)
			}
			for _, entry := range local.Entries {
				if entry.Dir {
					continue
				}
				b, e := s.Data(entry)
				if e != nil {
					return e
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if e = m.Store.PutFile(&scratch, entry.Hash, b, entry.Mode); e != nil {
					return e
				}
			}
			idx, shared, err := m.load(&t)
			if err != nil {
				return err
			}
			_, skills, err := m.resolve(&t, idx, shared)
			if err != nil {
				return err
			}
			base := layers
			base.Local = nil
			portable, err := m.resolveProfileWithAdoption(t, shared, skills, base, false)
			if err != nil {
				return err
			}
			// History and recovery state are observed separately by their owning
			// stores. Copying them into a snapshot must not restore authority/state.
			layers.History = nil
			layers.Recovery = false
			out := SnapshotConfiguration{Version: 2, SourceDigest: bundle.Snapshot, Layers: layers, LocalTree: local, Portable: portable}
			if m.Library != nil {
				out.SkillLibraryRoot = m.Library.Store.Root
			}
			out.ProfileLibrary = m.ProfileLibrary
			out.ReferencePackages, err = m.snapshotReferences(out, bundle, skills)
			if err != nil {
				return err
			}
			out.Digest = SnapshotConfigurationDigest(out)
			if err = ValidateSnapshotConfiguration(out, bundle, m.Store.Local); err != nil {
				return err
			}
			if err = ValidateSnapshotConfigurationBytes(out, bundle, m.Store.Data); err != nil {
				return err
			}
			current, err := m.ConfigurationDigest()
			if err != nil {
				return err
			}
			if current != layers.Digest {
				return result.Fail("PLAN_STALE", "快照交付前配置输入已变化", 3)
			}
			return use(bundle, t, out)
		})
	})
}
