package projectsnapshot

import (
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func nativeTrees(c Content) []workstore.Tree {
	out := []workstore.Tree{}
	for _, t := range c.Transaction.Targets {
		out = append(out, t.Before, t.After)
	}
	if c.Coordinated != nil {
		for _, t := range c.Coordinated.Targets {
			if strings.HasPrefix(t.Path, "native/") {
				out = append(out, t.Before, t.After)
			}
		}
	}
	for _, t := range publicationMemberTargets(c) {
		if strings.HasPrefix(t.Path, "native/") {
			out = append(out, t.Before, t.After)
		}
	}
	return out
}

func coordinatedMember(tree workstore.Tree, name string, file bool) workstore.Tree {
	out := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if file {
		if e, ok := tree.Entries[name]; ok && !e.Dir {
			out.Exists = true
			out.Entries[workstore.FileContent] = e
		}
		return out
	}
	if e, ok := tree.Entries[name]; ok && e.Dir {
		out.Exists = true
	}
	for p, e := range tree.Entries {
		if strings.HasPrefix(p, name+"/") {
			out.Entries[strings.TrimPrefix(p, name+"/")] = e
		}
	}
	return out
}

// A v4 record binds a complete common transaction and its exact compiler
// targets. Source/Local before-images belong to the private Git observation;
// complete native containers stay in native backup blobs, including siblings.
// A valid archive is still an intent until its owning coordinator commits.
func (a *Archive) validateCoordinated(c Content) error {
	bad := func() error {
		return result.Fail("SNAPSHOT_COORDINATED_INVALID", "共同回放快照的完整事务、源/本机或原生目标绑定不一致", 5)
	}
	p := c.Coordinated
	want := a.Binding.Project + "/" + a.Binding.Scope + "/" + a.Binding.Copy
	area := filepath.Join(a.Local, "native-mcp-private", workstore.Hash([]byte(a.Binding.NativeRoot+"\x00"+a.Binding.Project+"\x00"+a.Binding.Scope+"\x00"+a.Binding.Copy)), "snapshot-replay")
	if p == nil || p.Version != 1 || p.Kind != "coordinated-roots" || p.ID != c.Transaction.ID || p.Root != area || p.Identity != "snapshot-replay/"+want || p.Digest != workstore.BatchPlanDigest(*p) || len(p.Parents)+len(p.RemoveParents) != 0 || len(p.Roots) != 9 || len(p.Targets) != 9 || c.Trigger != "aidock replay" || c.Transaction.Kind != "native-targets" || c.Transaction.Digest != workstore.BatchPlanDigest(c.Transaction) {
		return bad()
	}
	if p.Action == "apply" {
		if p.Reference != "" || c.RestoredDraft || c.RestoreOf != "" {
			return bad()
		}
	} else if (p.Action != "rollback" && p.Action != "undo-rollback") || !c.RestoredDraft || !uuid.MatchString(p.Reference) || c.RestoreOf != p.Reference {
		return bad()
	}
	roots := map[string]workstore.RootBinding{
		"source": {Path: "source", Root: a.Binding.SourceRoot},
		"local":  {Path: "local", Root: source.LocalProfileRoot(a.Local, a.Binding.Project, a.Binding.Scope, a.Binding.Copy)},
	}
	for _, name := range []string{".claude", ".agents", ".codex", "CLAUDE.md", "AGENTS.md", ".mcp.json", ".aidock-mcp-ownership.json"} {
		key := "native/" + name
		roots[key] = workstore.RootBinding{Path: key, Root: filepath.Join(a.Binding.NativeRoot, name), File: name != ".claude" && name != ".agents" && name != ".codex", NativeProject: a.Binding.NativeRoot}
	}
	seen := map[string]bool{}
	for _, root := range p.Roots {
		want, ok := roots[root.Path]
		if !ok || seen[root.Path] || root != want {
			return bad()
		}
		seen[root.Path] = true
	}
	byKey := map[string]workstore.BatchTarget{}
	for _, t := range p.Targets {
		root, ok := roots[t.Path]
		if !ok || t.File != root.File {
			return bad()
		}
		if _, ok := byKey[t.Path]; ok {
			return bad()
		}
		byKey[t.Path] = t
		if !c.RestoredDraft && t.Path == "source" && workstore.Digest(t.After) != workstore.Digest(c.Source) {
			return bad()
		}
		if !c.RestoredDraft && t.Path == "local" && workstore.Digest(t.After) != workstore.Digest(c.Configuration.LocalTree) {
			return bad()
		}
	}
	for _, t := range c.Transaction.Targets {
		container, member := "", ""
		switch t.Path {
		case ".claude/skills":
			container, member = ".claude", "skills"
		case ".agents/skills":
			container, member = ".agents", "skills"
		case ".codex/config.toml":
			container, member = ".codex", "config.toml"
		case "CLAUDE.md", "AGENTS.md", ".mcp.json", ".aidock-mcp-ownership.json":
			container = t.Path
		default:
			return bad()
		}
		if t.File != (member != "skills") {
			return bad()
		}
		full := byKey["native/"+container]
		before, after := full.Before, full.After
		if member != "" {
			before, after = coordinatedMember(before, member, t.File), coordinatedMember(after, member, t.File)
		}
		if workstore.Digest(before) != workstore.Digest(t.Before) || workstore.Digest(after) != workstore.Digest(t.After) {
			return bad()
		}
	}
	return nil
}
