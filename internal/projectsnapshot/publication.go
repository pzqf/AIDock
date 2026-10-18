package projectsnapshot

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// PublicationCommit refers to one REAL shared coordinator. Transaction is the
// entire common plan, not a synthetic committed journal for this member. The
// archive stores bodies only for its own binding; foreign members remain hashes.
type PublicationCommit struct {
	Version     int                 `json:"schema_version"`
	Publication string              `json:"publication_id"`
	Publisher   Binding             `json:"publisher"`
	Members     []Binding           `json:"members"`
	Transaction workstore.BatchPlan `json:"transaction"`
	View        *PublicationView    `json:"applied_view,omitempty"`
}

// View belongs only to this archive's member, never to the other members.
type PublicationView struct {
	Executable    string                     `json:"executable"`
	ExecutableSHA string                     `json:"executable_sha256"`
	Sessions      []source.MCPSessionRequest `json:"sessions"`
}

func PublicationArea(local, project, scope, id string) string {
	return filepath.Join(local, "replica-publications-private", workstore.Hash([]byte(project+"/"+scope)), "runs", id)
}

func PublicationMemberPrefix(copy string) string { return "copies/" + copy + "/" }

// This factory derives every writable root from project/copy bindings. It
// never accepts arbitrary transaction roots or creates the private directory.
func PublicationCoordinator(local, project, scope, id string, members []Binding) (*workstore.Coordinator, error) {
	bad := func() (*workstore.Coordinator, error) {
		return nil, result.Fail("PUBLICATION_COMMIT_INVALID", "完整副本提交需要明确项目、作用域、批次和互不重叠的目标绑定", 5)
	}
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !bindingID.MatchString(project) || strings.TrimSpace(scope) == "" || !uuid.MatchString(id) || len(members) == 0 || len(members) > 32 {
		return bad()
	}
	control := filepath.Join(local, "replica-control", workstore.Hash([]byte(project+"/"+scope)))
	area := PublicationArea(local, project, scope, id)
	roots := []workstore.RootBinding{{Path: "control", Root: control}}
	seen := map[string]bool{}
	for _, b := range members {
		if !publicationBinding(b, project, scope) || seen[b.Copy] || workstore.Within(local, b.NativeRoot) || workstore.Within(b.SourceRoot, local) {
			return bad()
		}
		seen[b.Copy] = true
		prefix := PublicationMemberPrefix(b.Copy)
		roots = append(roots, workstore.RootBinding{Path: prefix + "source", Root: b.SourceRoot}, workstore.RootBinding{Path: prefix + "local", Root: source.LocalProfileRoot(local, project, scope, b.Copy)})
		for _, name := range []string{".claude", ".agents", ".codex", "CLAUDE.md", "AGENTS.md", ".mcp.json", ".aidock-mcp-ownership.json"} {
			roots = append(roots, workstore.RootBinding{Path: prefix + "native/" + name, Root: filepath.Join(b.NativeRoot, name), File: name != ".claude" && name != ".agents" && name != ".codex", NativeProject: b.NativeRoot})
		}
	}
	return &workstore.Coordinator{Local: area, Identity: "replica-full-publication/" + project + "/" + scope + "/" + id, Roots: roots, Blobs: &workstore.Store{Root: control, Local: area}, RecoveryCommand: "replica publication recover --publication " + id}, nil
}

func publicationBinding(b Binding, project, scope string) bool {
	return b.Project == project && b.Scope == scope && uuid.MatchString(b.Copy) && filepath.IsAbs(b.SourceRoot) && filepath.Clean(b.SourceRoot) == b.SourceRoot && filepath.Base(b.SourceRoot) == ".aidock" && b.NativeRoot == filepath.Dir(b.SourceRoot)
}

func (p PublicationCommit) Coordinator(local string) (*workstore.Coordinator, error) {
	return PublicationCoordinator(local, p.Publisher.Project, p.Publisher.Scope, p.Publication, p.Members)
}

func (p PublicationCommit) Validate(local string) error {
	bad := func() error {
		return result.Fail("PUBLICATION_COMMIT_INVALID", "完整副本提交证明的批次、发布者、目标或原始事务不一致", 5)
	}
	if p.Version != 1 || !publicationBinding(p.Publisher, p.Publisher.Project, p.Publisher.Scope) || !bindingID.MatchString(p.Publisher.Project) || strings.TrimSpace(p.Publisher.Scope) == "" {
		return bad()
	}
	for _, b := range p.Members {
		if b.Copy == p.Publisher.Copy || workstore.Within(p.Publisher.NativeRoot, b.NativeRoot) || workstore.Within(b.NativeRoot, p.Publisher.NativeRoot) {
			return bad()
		}
	}
	c, err := p.Coordinator(local)
	if err != nil {
		return err
	}
	t := p.Transaction
	if t.Action == "apply" {
		if t.Reference != "" {
			return bad()
		}
	} else if (t.Action != "rollback" && t.Action != "undo-rollback") || !uuid.MatchString(t.Reference) {
		return bad()
	}
	for _, target := range t.Targets {
		if strings.HasSuffix(target.Path, "/local") && workstore.Digest(target.Before) != workstore.Digest(target.After) {
			return bad()
		}
	}
	return c.ValidateManifest(t)
}

// State reads the common atomic journal without taking any target locks. The
// runtime caller holds its own native-project lock; taking all member locks
// there would invert the frozen-source reader's lock order.
func (p PublicationCommit) State(local string) (string, error) {
	if err := p.Validate(local); err != nil {
		return "", err
	}
	c, _ := p.Coordinator(local)
	js, err := c.History()
	if err != nil {
		return "", err
	}
	for _, j := range js {
		if j.Plan.ID != p.Transaction.ID {
			continue
		}
		actual, _ := json.Marshal(j.Plan)
		wanted, _ := json.Marshal(p.Transaction)
		if string(actual) != string(wanted) {
			return "", result.Fail("SNAPSHOT_TRANSACTION_MISMATCH", "项目快照与真实全批事务不同", 5)
		}
		return j.State, nil
	}
	return "unknown", nil
}

func (a *Archive) validatePublication(c Content) error {
	bad := func() error {
		return result.Fail("SNAPSHOT_PUBLICATION_INVALID", "全批快照的目标子集、冻结源或原生编译材料不符", 5)
	}
	p := c.Publication
	if p == nil || c.Coordinated != nil || c.Trigger != "replica publication" || c.Transaction.Kind != "native-targets" || c.Transaction.Version != 1 || c.Transaction.Digest != workstore.BatchPlanDigest(c.Transaction) || c.Transaction.ID != p.Transaction.ID || c.Transaction.Action != p.Transaction.Action || c.Transaction.Reference != p.Transaction.Reference || len(c.Transaction.Roots)+len(c.Transaction.Parents)+len(c.Transaction.RemoveParents) != 0 {
		return bad()
	}
	if err := p.Validate(a.Local); err != nil {
		return err
	}
	if p.View == nil || !filepath.IsAbs(p.View.Executable) || filepath.Clean(p.View.Executable) != p.View.Executable || !sha256ID.MatchString(p.View.ExecutableSHA) || p.View.Sessions == nil || len(p.View.Sessions) > 64 {
		return bad()
	}
	names := map[string]bool{}
	for _, s := range p.View.Sessions {
		if workstore.SafeRelative(s.Name) != nil || strings.ContainsAny(s.Name, "/\\") || s.Probe || names[s.Name] || !sha256ID.MatchString(s.ExpectedSHA256) || !sha256ID.MatchString(s.ExpectedReviewSHA256) || !uuid.MatchString(s.ExpectedRuntimeAttempt) {
			return bad()
		}
		names[s.Name] = true
	}
	found := false
	for _, b := range p.Members {
		if b == a.Binding {
			found = true
		}
	}
	if !found || p.Publisher.Project != a.Binding.Project || p.Publisher.Scope != a.Binding.Scope {
		return bad()
	}
	prefix := PublicationMemberPrefix(a.Binding.Copy)
	byPath := map[string]workstore.BatchTarget{}
	for _, t := range p.Transaction.Targets {
		if strings.HasPrefix(t.Path, prefix) {
			byPath[strings.TrimPrefix(t.Path, prefix)] = t
		}
	}
	if len(byPath) != 9 {
		return bad()
	}
	if p.Transaction.Action == "apply" {
		if c.RestoredDraft || c.RestoreOf != "" || workstore.Digest(byPath["source"].After) != workstore.Digest(c.Source) || workstore.Digest(byPath["local"].After) != workstore.Digest(c.Configuration.LocalTree) {
			return bad()
		}
	} else if !c.RestoredDraft || c.RestoreOf != p.Transaction.Reference {
		return bad()
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
		full := byPath["native/"+container]
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

func publicationMemberTargets(c Content) []workstore.BatchTarget {
	out := []workstore.BatchTarget{}
	if c.Publication == nil {
		return out
	}
	prefix := PublicationMemberPrefix(c.Binding.Copy)
	for _, t := range c.Publication.Transaction.Targets {
		if strings.HasPrefix(t.Path, prefix) {
			t.Path = strings.TrimPrefix(t.Path, prefix)
			out = append(out, t)
		}
	}
	return out
}
