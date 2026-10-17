package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/nativeconfig"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const connectionLedgerPath = ".aidock-mcp-ownership.json"

type MCPConnectionRequest struct {
	Action  string                   `json:"action"`
	Tools   []string                 `json:"tools"`
	CheckID string                   `json:"check_id,omitempty"`
	Session source.MCPSessionRequest `json:"session"`
}

type ownedConnection struct {
	Plugin     string                   `json:"plugin"`
	SHA256     string                   `json:"sha256"`
	Session    source.MCPSessionRequest `json:"session"`
	Executable string                   `json:"executable"`
	Home       string                   `json:"home"`
	ConfigSHA  string                   `json:"configuration_sha256"`
	AppliedKey string                   `json:"applied_binding,omitempty"`
}

type connectionTool struct {
	AdapterVersion string                     `json:"adapter_version"`
	Contract       nativeconfig.Contract      `json:"contract"`
	Fields         map[string]ownedConnection `json:"fields"`
}

type connectionLedger struct {
	Version    int                       `json:"schema_version"`
	Owner      string                    `json:"owner"`
	Scope      string                    `json:"scope_id"`
	Copy       string                    `json:"copy"`
	SourceRoot string                    `json:"source_root"`
	Tools      map[string]connectionTool `json:"tools"`
}

type MCPConnectionPlan struct {
	Version       int                            `json:"schema_version"`
	ID            string                         `json:"id"`
	Root          string                         `json:"root"`
	Request       MCPConnectionRequest           `json:"request"`
	Executable    string                         `json:"executable"`
	ExecutableSHA string                         `json:"executable_sha256,omitempty"`
	Home          string                         `json:"home"`
	Configuration *source.MCPClientConfiguration `json:"configuration,omitempty"`
	Transaction   workstore.BatchPlan            `json:"transaction"`
	Changes       int                            `json:"changes"`
	Digest        string                         `json:"digest"`
	Boundary      string                         `json:"boundary"`
}

func connectionDigest(p MCPConnectionPlan) string { p.Digest = ""; return workstore.Hash(jsonData(p)) }

func (m *Manager) connectionArea() string {
	return filepath.Join(m.Source.Store.Local, "native-mcp-private", workstore.Hash([]byte(m.Root+"\x00"+m.Source.Owner+"\x00"+m.Source.Scope+"\x00"+m.Source.Copy)))
}

func (m *Manager) connectionStore() *workstore.Store {
	return &workstore.Store{Root: m.Root, Local: m.connectionArea()}
}

func (m *Manager) connectionBatch() *workstore.Batch {
	paths := []string{connectionLedgerPath}
	for _, a := range Adapters() {
		if a.MCPConfig != nil {
			paths = append(paths, a.MCPConfig.Path)
		}
	}
	return &workstore.Batch{Boundary: m.Root, Local: m.connectionArea(), Identity: "mcp/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Paths: paths, FilePaths: paths, Blobs: m.connectionStore(), Hook: m.connectionHook}
}

func (m *Manager) connectionLedger() (connectionLedger, error) {
	out := connectionLedger{Version: 1, Owner: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, Tools: map[string]connectionTool{}}
	data, present, err := readConfiguration(filepath.Join(m.Root, connectionLedgerPath))
	if err != nil {
		return out, err
	}
	if !present {
		return out, nil
	}
	return m.decodeConnectionLedger(data)
}

func (m *Manager) decodeConnectionLedger(data []byte) (connectionLedger, error) {
	var out connectionLedger
	if err := strict(data, &out); err != nil || out.Version != 1 || out.Owner != m.Source.Owner || out.Scope != m.Source.Scope || out.Copy != m.Source.Copy || out.SourceRoot != m.Source.Store.Root || out.Tools == nil {
		return out, result.Fail("MCP_CONNECTION_OWNERSHIP_INVALID", "原生 MCP 字段归属记录损坏或属于其它项目副本；不接管文件", 5)
	}
	for id, tool := range out.Tools {
		as, e := selectAdapters([]string{id})
		if e != nil || len(as) != 1 || as[0].MCPConfig == nil || tool.AdapterVersion != as[0].Version || workstore.Hash(jsonData(tool.Contract)) != workstore.Hash(jsonData(*as[0].MCPConfig)) || tool.Fields == nil {
			return out, result.Fail("MCP_CONNECTION_OWNERSHIP_INVALID", "字段归属的适配器合同无效；保留原文件", 5)
		}
		for name, field := range tool.Fields {
			if name != "aidock-"+field.Plugin || !validName.MatchString(field.Plugin) || !validDigest.MatchString(field.SHA256) || !validDigest.MatchString(field.ConfigSHA) || field.Session.Name != field.Plugin || field.Session.Probe || !filepath.IsAbs(field.Executable) || !filepath.IsAbs(field.Home) || field.AppliedKey != "" && !validDigest.MatchString(field.AppliedKey) {
				return out, result.Fail("MCP_CONNECTION_OWNERSHIP_INVALID", "字段归属含无效启动绑定；保留原文件", 5)
			}
		}
	}
	return out, nil
}

// connectionVersion only consumes a prior explicit discovery/version receipt.
// Reading or previewing a connection never invokes a vendor CLI.
func (m *Manager) connectionVersion(id string, adapters []Adapter) error {
	r, err := m.LatestMCPConnectionVersions()
	if err != nil {
		return err
	}
	if r == nil || r.ID != id {
		return result.Fail("CHECK_STALE", "需要最新的 MCP 客户端版本记录；新的失败检查不能沿用旧成功", 3)
	}
	created, ce := time.Parse(time.RFC3339Nano, r.Created)
	expires, ee := time.Parse(time.RFC3339Nano, r.Expires)
	if ce != nil || ee != nil || created.After(time.Now()) || !time.Now().Before(expires) || expires.Sub(created) > 24*time.Hour {
		return result.Fail("CHECK_STALE", "需要当前副本 24 小时内显式取得的精确工具版本记录", 3)
	}
	for _, a := range adapters {
		found := false
		for _, t := range r.Tools {
			if t.Tool != a.ID {
				continue
			}
			binary, e := m.probes().resolve(t.Tool)
			found = e == nil && hasVersion(a.Verified, t.Version) && binary.Path == t.Executable && binary.Digest == t.BinaryDigest
		}
		if !found {
			return result.Fail("NATIVE_CHECK_REQUIRED", "所选工具缺少受支持精确版本的当前程序记录；先显式检查工具", 4)
		}
	}
	return nil
}

func executableDigest(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "需要稳定的绝对 AIDock 程序路径", 2)
	}
	if err := workstore.CheckLinks(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", result.Fail("PATH_UNSAFE", "AIDock 程序须为普通文件", 4)
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Manager) compileConnection(req MCPConnectionRequest, configuration *source.MCPClientConfiguration, executable, home string) ([]workstore.BatchTarget, error) {
	return m.compileConnectionSet(req.Tools, []MCPConnectionRequest{req}, []*source.MCPClientConfiguration{configuration}, executable, home, false)
}

func (m *Manager) compileConnectionSet(tools []string, requests []MCPConnectionRequest, configurations []*source.MCPClientConfiguration, executable, home string, reconcile bool) ([]workstore.BatchTarget, error) {
	return m.compileConnectionImages(nil, tools, requests, configurations, executable, home, reconcile)
}

// A restore may regenerate only managed launchers against its immutable
// intended before-image, preserving original manual fields and formatting.
// images is internal backup material, never accepted from a CLI/API request.
func (m *Manager) compileConnectionImages(images map[string]workstore.Tree, tools []string, requests []MCPConnectionRequest, configurations []*source.MCPClientConfiguration, executable, home string, reconcile bool) ([]workstore.BatchTarget, error) {
	as, err := selectAdapters(tools)
	if err != nil {
		return nil, err
	}
	store := m.connectionStore()
	capture := func(path string) (workstore.Tree, error) {
		if images == nil {
			return store.CaptureFile(filepath.Join(m.Root, path))
		}
		tree, ok := images[path]
		if !ok {
			return workstore.Tree{}, result.Fail("BACKUP_INVALID", "恢复编译缺少明确原生文件镜像", 5)
		}
		return workstore.Clone(tree), nil
	}
	ledgerBefore, err := capture(connectionLedgerPath)
	if err != nil {
		return nil, err
	}
	ledger := connectionLedger{Version: 1, Owner: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, Tools: map[string]connectionTool{}}
	if ledgerBefore.Exists {
		// Compile from the exact ownership snapshot used by the transaction.
		// A separate live read could otherwise authorize a different ledger
		// from the bytes subsequently captured as the before-image.
		data, e := store.Data(ledgerBefore.Entries[workstore.FileContent])
		if e != nil {
			return nil, e
		}
		ledger, err = m.decodeConnectionLedger(data)
		if err != nil {
			return nil, err
		}
	}
	targets := []workstore.BatchTarget{}
	for _, a := range as {
		if a.MCPConfig == nil {
			return nil, result.Fail("CONFIG_CONTRACT_UNSUPPORTED", "缺少工具 MCP 合同", 4)
		}
		contract := *a.MCPConfig
		var data []byte
		var present bool
		var e error
		if images == nil {
			data, present, e = readConfiguration(filepath.Join(m.Root, contract.Path))
		} else {
			var image workstore.Tree
			image, e = capture(contract.Path)
			if e == nil {
				present = image.Exists
				if present {
					data, e = store.Data(image.Entries[workstore.FileContent])
				}
			}
		}
		if e != nil {
			return nil, e
		}
		if !present {
			if contract.Format == "json" {
				data = []byte("{}\n")
			} else {
				data = []byte{}
			}
		}
		observed, e := nativeconfig.Inspect(contract, data)
		if e != nil {
			return nil, e
		}
		tool, exists := ledger.Tools[a.ID]
		if !exists {
			tool = connectionTool{AdapterVersion: a.Version, Contract: contract, Fields: map[string]ownedConnection{}}
		}
		fields := map[string]string{}
		for _, f := range observed.Fields {
			fields[f.Name] = f.SHA256
		}
		for n, owned := range tool.Fields {
			if fields[n] != owned.SHA256 {
				return nil, result.Fail("CONFIG_MANAGED_DRIFT", "已托管 MCP 字段已被外部修改或移除；保留手改，请先协调", 3)
			}
		}
		owned := map[string]string{}
		desired := map[string]json.RawMessage{}
		if reconcile {
			for name, field := range tool.Fields {
				owned[name] = field.SHA256
			}
		}
		for i, req := range requests {
			name := "aidock-" + req.Session.Name
			if old, ok := tool.Fields[name]; ok {
				owned[name] = old.SHA256
			}
			if req.Action == "install" {
				configuration := configurations[i]
				value := map[string]any{"command": configuration.Command, "args": configuration.Args}
				if a.ID == "claude-code" {
					value["type"] = "stdio"
				}
				desired[name] = json.RawMessage(jsonData(value))
			} else if _, ok := tool.Fields[name]; !ok {
				return nil, result.Fail("MCP_CONNECTION_NOT_OWNED", "所选工具没有此项目登记的连接；不删除未托管项", 4)
			}
		}

		merged, e := nativeconfig.Merge(contract, data, owned, desired)
		if e != nil {
			return nil, e
		}
		before, e := capture(contract.Path)
		if e != nil {
			return nil, e
		}
		if present && before.Entries[workstore.FileContent].Hash != workstore.Hash(data) {
			return nil, result.Fail("PLAN_STALE", "读取期间原生配置已变化", 3)
		}
		after := workstore.Clone(before)
		// Reconciling an empty effective set must not create an empty native
		// configuration file where none existed. Still include the absent
		// before/after target so a concurrent external creation makes it stale.
		if present || len(desired) > 0 {
			after.Exists = true
			mode := uint32(0600)
			if before.Exists {
				mode = before.Entries[workstore.FileContent].Mode
			}
			if err := store.PutFile(&after, workstore.FileContent, merged.Bytes, mode); err != nil {
				return nil, err
			}
		}
		if reconcile {
			tool.Fields = map[string]ownedConnection{}
		}
		for i, req := range requests {
			name := "aidock-" + req.Session.Name
			if req.Action == "install" {
				tool.Fields[name] = ownedConnection{Plugin: req.Session.Name, SHA256: merged.Owned[name], Session: req.Session, Executable: executable, Home: home, ConfigSHA: workstore.Hash(jsonData(configurations[i])), AppliedKey: configurations[i].AppliedKey}
			} else {
				delete(tool.Fields, name)
			}
		}

		if len(tool.Fields) == 0 {
			delete(ledger.Tools, a.ID)
		} else {
			ledger.Tools[a.ID] = tool
		}
		targets = append(targets, workstore.BatchTarget{Path: contract.Path, File: true, Before: before, After: after})
	}
	ledgerAfter := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if len(ledger.Tools) > 0 {
		ledgerAfter.Exists = true
		if err := store.PutFile(&ledgerAfter, workstore.FileContent, jsonData(ledger), 0600); err != nil {
			return nil, err
		}
	}
	return append(targets, workstore.BatchTarget{Path: connectionLedgerPath, File: true, Before: ledgerBefore, After: ledgerAfter}), nil
}

func (m *Manager) MCPConnectionPlan(ctx context.Context, req MCPConnectionRequest, executable, home string) (MCPConnectionPlan, error) {
	var p MCPConnectionPlan
	if (req.Action != "install" && req.Action != "remove") || !validName.MatchString(req.Session.Name) || req.Session.Probe || m.Source.Copy == "" {
		return p, result.Fail("INVALID_INPUT", "明确选择 install/remove、插件名称和当前项目副本；预览不执行插件", 2)
	}
	as, err := selectAdapters(req.Tools)
	if err != nil {
		return p, err
	}
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return p, err
	}
	p = MCPConnectionPlan{Version: 1, Root: m.Root, Request: req, Executable: executable, Home: home, Boundary: "仅在明确确认后安装/移除所选工具的项目级受管 MCP 字段与归属记录；保留其它字段。原始字节仅备份到受限本机目录，不进入源/普通导出。安装不启动插件，不提升工具 L3/L4；Tier 0 非隔离，客户端之后可能启动已授权插件。回退与撤销同样先预览确认。"}
	prepare := func(configuration *source.MCPClientConfiguration) error {
		if req.Action == "install" {
			if err := m.connectionVersion(req.CheckID, as); err != nil {
				return err
			}
			var err error
			p.ExecutableSHA, err = executableDigest(executable)
			if err != nil {
				return err
			}
		}
		p.Configuration = configuration
		targets, err := m.compileConnection(req, configuration, executable, home)
		if err != nil {
			return err
		}
		p.Transaction, err = m.connectionBatch().Preview("apply", targets)
		if err != nil {
			return err
		}
		p.ID = p.Transaction.ID
		for _, target := range targets {
			p.Changes += len(workstore.Changes(target.Before, target.After))
		}
		p.Digest = connectionDigest(p)
		return save(filepath.Join(m.connectionArea(), "connection-plans", p.ID+".json"), jsonData(p))
	}
	if req.Action == "install" {
		err = m.Source.WithMCPClientConfig(ctx, req.Session, executable, home, func(c source.MCPClientConfiguration) error { return prepare(&c) })
	} else {
		err = m.Source.Store.Locked(func() error { return prepare(nil) })
	}
	return p, err
}

func (m *Manager) loadConnection(id, digest string) (MCPConnectionPlan, error) {
	var p MCPConnectionPlan
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return p, result.Fail("INVALID_INPUT", "需要连接预览 UUID 与摘要", 2)
	}
	data, err := workstore.ReadFile(filepath.Join(m.connectionArea(), "connection-plans", id+".json"))
	if err != nil {
		return p, err
	}
	if strict(data, &p) != nil || p.Version != 1 || p.ID != id || p.Root != m.Root || p.Digest != digest || connectionDigest(p) != digest || p.Transaction.ID != id {
		return p, result.Fail("PLAN_STALE", "原生连接预览身份或摘要不匹配", 3)
	}
	return p, nil
}

func (m *Manager) ApplyMCPConnection(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return out, err
	}
	p, err := m.loadConnection(id, digest)
	if err != nil {
		return out, err
	}
	if p.Changes == 0 {
		return out, result.Fail("MCP_CONNECTION_CURRENT", "所选连接已一致，没有需要写入的变化", 3)
	}
	apply := func(c *source.MCPClientConfiguration) error {
		if p.Request.Action == "install" {
			as, e := selectAdapters(p.Request.Tools)
			if e != nil {
				return e
			}
			if e = m.connectionVersion(p.Request.CheckID, as); e != nil {
				return e
			}
			h, e := executableDigest(p.Executable)
			if e != nil {
				return e
			}
			if h != p.ExecutableSHA || workstore.Hash(jsonData(c)) != workstore.Hash(jsonData(p.Configuration)) {
				return result.Fail("PLAN_STALE", "AIDock 程序或当前客户端启动配置已变化", 3)
			}
		}
		targets, e := m.compileConnection(p.Request, c, p.Executable, p.Home)
		if e != nil {
			return e
		}
		if workstore.Hash(jsonData(targets)) != workstore.Hash(jsonData(p.Transaction.Targets)) {
			return result.Fail("PLAN_STALE", "原生配置、归属或生成结果已变化；请重新预览", 3)
		}
		var err error
		out, err = m.connectionBatch().Apply(ctx, p.Transaction.ID, p.Transaction.Digest)
		return err
	}
	if p.Request.Action == "install" {
		err = m.Source.WithMCPClientConfig(ctx, p.Request.Session, p.Executable, p.Home, func(c source.MCPClientConfiguration) error { return apply(&c) })
	} else if p.Request.Action == "remove" {
		err = m.Source.Store.Locked(func() error { return apply(nil) })
	} else {
		err = result.Fail("INVALID_INPUT", "未知连接动作", 2)
	}
	return out, err
}

func (m *Manager) MCPConnectionHistory() ([]workstore.BatchJournal, error) {
	return m.connectionBatch().History()
}
func (m *Manager) RecoverMCPConnections() (int, error) {
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return 0, err
	}
	return m.connectionBatch().Recover()
}

// A reversal can reinstate one plugin across several clients. Reintroduced
// launchers must still be authorized under their original expected receipts;
// a stale launcher must be regenerated instead of silently restored.
func (m *Manager) withConnectionRestore(ctx context.Context, p workstore.BatchPlan, commit func() error) error {
	var before, after connectionLedger
	for _, target := range p.Targets {
		if target.Path != connectionLedgerPath {
			continue
		}
		for i, tree := range []workstore.Tree{target.Before, target.After} {
			value := connectionLedger{Tools: map[string]connectionTool{}}
			if tree.Exists {
				store := &workstore.Store{Root: filepath.Join(m.Root, connectionLedgerPath), Local: m.connectionArea()}
				data, err := store.Data(tree.Entries[workstore.FileContent])
				if err != nil {
					return err
				}
				value, err = m.decodeConnectionLedger(data)
				if err != nil {
					return err
				}
			}
			if i == 0 {
				before = value
			} else {
				after = value
			}
		}
	}
	restores := map[string]ownedConnection{}
	for tool, fields := range after.Tools {
		for name, next := range fields.Fields {
			previous := before.Tools[tool].Fields[name]
			if previous.ConfigSHA == next.ConfigSHA {
				continue
			}
			if old, ok := restores[next.Plugin]; ok && (workstore.Hash(jsonData(old.Session)) != workstore.Hash(jsonData(next.Session)) || old.Executable != next.Executable || old.Home != next.Home || old.ConfigSHA != next.ConfigSHA) {
				return result.Fail("MCP_CONNECTION_OWNERSHIP_INVALID", "恢复含不一致的插件启动绑定", 5)
			}
			restores[next.Plugin] = next
		}
	}
	if len(restores) == 0 {
		return m.Source.Store.Locked(commit)
	}
	names := make([]string, 0, len(restores))
	for name := range restores {
		names = append(names, name)
	}
	sort.Strings(names)
	requests := []source.MCPSessionRequest{}
	first := restores[names[0]]
	for _, name := range names {
		item := restores[name]
		if item.Executable != first.Executable || item.Home != first.Home {
			return result.Fail("MCP_CONNECTION_OWNERSHIP_INVALID", "恢复含不同程序或数据根的启动绑定", 5)
		}
		requests = append(requests, item.Session)
	}
	if first.AppliedKey != "" {
		for _, name := range names {
			if restores[name].AppliedKey != first.AppliedKey {
				return result.Fail("APPLIED_VIEW_RESTORE_REVIEW_REQUIRED", "恢复含多个不同已应用视图，须分别重新审查，不能按当前草稿推测授权", 4)
			}
		}
		a, err := m.loadAppliedRecord(ctx, first.AppliedKey)
		if err != nil {
			return err
		}
		if first.Executable != a.Plan.Executable || first.Home != a.Plan.Home {
			return result.Fail("APPLIED_VIEW_INVALID", "恢复程序或数据根与成功应用不同", 5)
		}
		return m.Source.WithFrozenMCPClientConfigs(ctx, a.Frozen, requests, first.Executable, first.Home, func(configurations []source.MCPClientConfiguration) error {
			for i, c := range configurations {
				c = m.appliedConfiguration(c, first.AppliedKey)
				if workstore.Hash(jsonData(c)) != restores[names[i]].ConfigSHA {
					return result.Fail("PLAN_STALE", "已应用连接授权或启动参数变化，不能直接恢复旧启动器", 3)
				}
			}
			if err := m.appliedArchiveCurrent(ctx, a); err != nil {
				return err
			}
			return commit()
		})
	}
	for _, name := range names {
		if restores[name].AppliedKey != "" {
			return result.Fail("APPLIED_VIEW_RESTORE_REVIEW_REQUIRED", "恢复混合了当前草稿与已应用视图，须先协调启动绑定", 4)
		}
	}
	return m.Source.WithMCPClientConfigs(ctx, requests, first.Executable, first.Home, func(configurations []source.MCPClientConfiguration) error {
		for i, c := range configurations {
			if workstore.Hash(jsonData(c)) != restores[names[i]].ConfigSHA {
				return result.Fail("PLAN_STALE", "原授权启动器已变化，请重新审查并安装连接", 3)
			}
		}
		return commit()
	})
}

func (m *Manager) ReverseMCPConnection(ctx context.Context, transaction string) (workstore.BatchPlan, error) {
	if !validID.MatchString(transaction) {
		return workstore.BatchPlan{}, result.Fail("INVALID_INPUT", "需要当前绑定的连接事务 UUID", 2)
	}
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return workstore.BatchPlan{}, err
	}
	p, err := m.connectionBatch().Reverse(transaction)
	if err != nil {
		return p, err
	}
	return p, m.withConnectionRestore(ctx, p, func() error { return nil })
}

func (m *Manager) ApplyMCPConnectionReverse(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return out, err
	}
	p, err := m.connectionBatch().LoadPlan(id)
	if err != nil {
		return out, err
	}
	if (p.Action != "rollback" && p.Action != "undo-rollback") || p.Digest != digest {
		return out, result.Fail("PLAN_KIND_MISMATCH", "需要连接回退或撤销回退的当前摘要", 2)
	}
	err = m.withConnectionRestore(ctx, p, func() error { var e error; out, e = m.connectionBatch().Apply(ctx, id, digest); return e })
	return out, err
}
