// Package generator prepares and validates an offline exchange with a declared
// external skill generator. No method in this package launches a process.
package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const Policy = "offline-skill-mirror-exchange-v1"
const Protocol = "aidock-skill-mirror-v1"
const Boundary = "仅准备外部生成器的离线暂存交换和检查返回的完整技能镜像；不启动程序，不安装工具文件，不修改项目或副本基线。程序版本为声明值，完整运行目录以字节锁定。返回内容即使通过校验也不证明它由该程序生成，不授予执行或发布权限。"

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var label = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Contract deliberately has no shell, free arguments, environment or MCP fields.
// The first exchange protocol supports exact skill packages in two fixed roots.
type Contract struct {
	Version  int      `json:"schema_version"`
	ID       string   `json:"id"`
	Release  string   `json:"declared_version"`
	Protocol string   `json:"protocol"`
	Runtime  string   `json:"runtime_root"`
	Entry    string   `json:"entry_relative"`
	Tools    []string `json:"tools"`
	Network  string   `json:"network"`
	Children string   `json:"child_processes"`
}
type Preview struct {
	Version          int            `json:"schema_version"`
	Policy           string         `json:"policy"`
	AdapterPath      string         `json:"adapter_path"`
	AdapterHash      string         `json:"adapter_sha256"`
	Contract         Contract       `json:"contract"`
	Runtime          workstore.Tree `json:"runtime"`
	ExecutableHash   string         `json:"executable_sha256"`
	Source           source.Export  `json:"source"`
	Input            workstore.Tree `json:"input"`
	Expected         workstore.Tree `json:"expected_output"`
	Digest           string         `json:"digest"`
	ExecutionReady   bool           `json:"execution_ready"`
	PublicationReady bool           `json:"publication_ready"`
	Boundary         string         `json:"boundary"`
}
type Manager struct {
	Source *source.Manager
	// Internal fault injection is not configurable through CLI/API.
	hook func(string) error
}

func New(s *source.Manager) *Manager           { return &Manager{Source: s} }
func encode(v any) []byte                      { b, _ := json.Marshal(v); return b }
func fail(code, detail string, exit int) error { return result.Fail(code, detail, exit) }
func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("JSON 必须只有一个对象")
	}
	return nil
}
func mirror(tool string) string {
	switch tool {
	case "claude-code":
		return ".claude/skills"
	case "codex-cli":
		return ".agents/skills"
	}
	return ""
}
func overlap(a, b string) bool { return workstore.Within(a, b) || workstore.Within(b, a) }
func (m *Manager) validContract(c Contract) error {
	if c.Version != 1 || !label.MatchString(c.ID) || c.Release == "" || len(c.Release) > 128 || strings.ContainsAny(c.Release, "\r\n\x00") || c.Protocol != Protocol || c.Network != "deny" || c.Children != "deny" || len(c.Tools) < 1 || len(c.Tools) > 2 {
		return fail("GENERATOR_CONTRACT_INVALID", "只支持有明确版本、禁止网络/子进程的完整技能镜像协议；不接收自由命令、环境或 MCP 配置", 2)
	}
	if !filepath.IsAbs(c.Runtime) || filepath.Clean(c.Runtime) != c.Runtime || overlap(c.Runtime, m.Source.Store.Root) || overlap(c.Runtime, m.Source.Store.Local) {
		return fail("GENERATOR_CONTRACT_INVALID", "运行依赖目录须为独立规范绝对路径，不能与源或 Local 重叠", 2)
	}
	if err := workstore.SafeRelative(c.Entry); err != nil {
		return fail("GENERATOR_CONTRACT_INVALID", err.Error(), 2)
	}
	seen := map[string]bool{}
	for _, tool := range c.Tools {
		if mirror(tool) == "" || seen[tool] {
			return fail("GENERATOR_CONTRACT_INVALID", "工具未知或重复", 2)
		}
		seen[tool] = true
	}
	return nil
}
func previewDigest(p Preview) string { p.Digest = ""; return workstore.Hash(encode(p)) }

func joinTree(dst *workstore.Tree, prefix string, src workstore.Tree) error {
	if err := workstore.SafeRelative(prefix); err != nil {
		return err
	}
	for dir := prefix; dir != "."; dir = path.Dir(dir) {
		if old, ok := dst.Entries[dir]; ok && !old.Dir {
			return fmt.Errorf("目录槽位被占用")
		}
		dst.Entries[dir] = workstore.Directory()
	}
	seen := map[string]bool{}
	for key := range dst.Entries {
		seen[strings.ToLower(key)] = true
	}
	for rel, e := range src.Entries {
		if err := workstore.SafeRelative(rel); err != nil {
			return err
		}
		key := prefix + "/" + rel
		if seen[strings.ToLower(key)] {
			return fmt.Errorf("路径重复或大小写冲突：%s", key)
		}
		seen[strings.ToLower(key)] = true
		dst.Entries[key] = e
	}
	return nil
}
func (m *Manager) Inspect(ctx context.Context, adapter string) (Preview, error) {
	p := Preview{Version: 1, Policy: Policy, AdapterPath: adapter, Boundary: Boundary}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if !filepath.IsAbs(adapter) || filepath.Clean(adapter) != adapter {
		return p, fail("GENERATOR_CONTRACT_INVALID", "适配文件须为规范绝对路径", 2)
	}
	b, err := workstore.ReadFile(adapter)
	if err != nil {
		return p, err
	}
	if len(b) > 64<<10 || strict(b, &p.Contract) != nil {
		return p, fail("GENERATOR_CONTRACT_INVALID", "适配文件无效、超过 64 KiB 或含未声明字段", 2)
	}
	if err = m.validContract(p.Contract); err != nil {
		return p, err
	}
	p.AdapterHash = workstore.Hash(b)
	p.Runtime, err = m.Source.Store.Capture(p.Contract.Runtime)
	if err != nil {
		return p, err
	}
	exe, ok := p.Runtime.Entries[p.Contract.Entry]
	if !p.Runtime.Exists || !ok || exe.Dir || exe.Size == 0 {
		return p, fail("GENERATOR_DEPENDENCY_MISSING", "运行目录或声明程序缺失；不能从 PATH 查找替代", 5)
	}
	p.ExecutableHash = exe.Hash
	p.Source, err = m.Source.Export()
	if err != nil {
		return p, err
	}
	if p.Source.Preflight.Status == "FAIL" {
		return p, fail("GENERATOR_INPUT_INVALID", "源检查有阻断，不能暂存", 5)
	}
	if len(p.Source.Authorities) != 0 || p.Source.BridgeDigest != "" {
		return p, fail("GENERATOR_AUTHORITY_UNSUPPORTED", "当前交换协议只支持 owned 源；外部桥接需要独立字段与所有权适配", 4)
	}
	p.Input = workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
	p.Expected = workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
	names := map[string]bool{}
	for _, pkg := range p.Source.Packages {
		if pkg.Layer != "owned" || pkg.ReadOnly || pkg.Skill.Source.Mode != "owned" {
			return p, fail("GENERATOR_AUTHORITY_UNSUPPORTED", "当前交换协议不接管继承或外部包", 4)
		}
		name := pkg.Skill.Name
		if workstore.SafeRelative(name) != nil || strings.Contains(name, "/") || names[strings.ToLower(name)] {
			return p, fail("GENERATOR_INPUT_INVALID", "技能名称无效或冲突", 5)
		}
		names[strings.ToLower(name)] = true
		if err = joinTree(&p.Input, "skills/"+name, pkg.Tree); err != nil {
			return p, err
		}
	}
	if len(names) == 0 {
		return p, fail("GENERATOR_INPUT_EMPTY", "先启用至少一个完整技能包", 4)
	}
	sort.Strings(p.Contract.Tools)
	for _, tool := range p.Contract.Tools {
		for _, pkg := range p.Source.Packages {
			if err = joinTree(&p.Expected, mirror(tool)+"/"+pkg.Skill.Name, pkg.Tree); err != nil {
				return p, err
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	// Capture the complete runtime and descriptor again: never approve mixed bytes.
	after, err := m.Source.Store.Capture(p.Contract.Runtime)
	if err != nil {
		return p, err
	}
	b, err = workstore.ReadFile(adapter)
	if err != nil {
		return p, err
	}
	if workstore.Digest(after) != workstore.Digest(p.Runtime) || workstore.Hash(b) != p.AdapterHash {
		return p, fail("GENERATOR_STAGE_STALE", "适配文件或依赖在检查期间变化", 3)
	}
	sourceAfter, err := m.Source.Export()
	if err != nil {
		return p, err
	}
	if string(encode(sourceAfter)) != string(encode(p.Source)) {
		return p, fail("GENERATOR_STAGE_STALE", "源在检查期间变化；结果未接受", 3)
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	p.Digest = previewDigest(p)
	return p, nil
}
