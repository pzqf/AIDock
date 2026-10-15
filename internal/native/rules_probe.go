package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/workstore"
)

// This evidence is deliberately separate from L3: it covers instruction-file
// discovery in a fresh empty-config session, not the bound project's session or
// the model's use of its contents. No turn/start or model request is sent.
type RuleLoad struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

const ruleDocumentBudget = 32 * 1024

func (m *Manager) inspectRuleLoad(ctx context.Context, target Target, tool ToolCheck, run string, explicit bool) *RuleLoad {
	r := &RuleLoad{Status: "UNKNOWN", Path: target.Rule.Path, Code: "RULES_PROBE_REQUIRED", Detail: "尚未显式验证规则文件的隔离会话加载"}
	entry, ok := target.Rule.After.Entries[workstore.FileContent]
	if ok {
		r.SHA256 = entry.Hash
	}
	if !explicit {
		return r
	}
	if target.Status == "FAIL" || !ok {
		r.Code, r.Detail = "RULES_PROBE_BLOCKED", "规则候选有阻断项，未启动规则会话"
		return r
	}
	if !tool.Installed || !hasVersion(target.Adapter.RuleVersions, tool.Version) {
		r.Code, r.Detail = "RULES_PROTOCOL_UNSUPPORTED", "当前工具精确版本尚无已验收的无模型规则加载协议；不以技能发现代替"
		return r
	}
	data, err := m.Source.Store.Data(entry)
	if err != nil {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_BACKUP_INVALID", "规则候选备份不可读取"
		return r
	}
	if len(data) > ruleDocumentBudget {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_DOCUMENT_BUDGET", "规则与手写内容合计超过独立验证的 32 KiB 指令预算，不能证明完整候选可加载"
		return r
	}
	project, home := filepath.Join(run, target.Adapter.ID, "rules-project"), filepath.Join(run, target.Adapter.ID, "rules-home")
	// A separate repository and home prevent skill discovery's caches or any
	// caller's credentials, AGENTS.override.md, hooks or MCP configuration from
	// supplying the rule evidence.
	if err = m.prepareProbe(project, home, Target{Adapter: target.Adapter}); err != nil {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_PROBE_PREPARE_FAILED", "无法建立独立规则验证目录"
		return r
	}
	path := filepath.Join(project, target.Rule.Path)
	if err = os.WriteFile(path, data, 0600); err != nil {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_PROBE_PREPARE_FAILED", "无法保存独立规则候选"
		return r
	}
	ops := m.probes()
	if ops.rules == nil {
		r.Code, r.Detail = "RULES_PROTOCOL_UNSUPPORTED", "没有可用的规则加载检查实现"
		return r
	}
	sources, err := ops.rules(ctx, toolBinary{tool.Executable, tool.BinaryDigest}, project, home)
	if err != nil {
		r.Code, r.Detail = "RULES_LOAD_FAILED", probeFailure(err)+"；没有发送模型请求"
		return r
	}
	after, err := workstore.ReadFile(path)
	if err != nil || !bytes.Equal(data, after) {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_PROBE_INPUT_CHANGED", "检查期间规则候选字节变化，证据无效"
		return r
	}
	if len(sources) != 1 || !samePath(sources[0], path) {
		r.Status, r.Code, r.Detail = "FAIL", "RULES_SOURCE_MISMATCH", "工具返回的指令来源与唯一候选文件不一致；不记录临时目录或未知来源内容"
		return r
	}
	r.Status, r.Code, r.Detail = "PASS", "RULES_ISOLATED_LOAD", "临时会话已返回此候选规则文件的指令来源，检查前后字节一致；未验证实际项目优先级、正文遵从或旧会话"
	return r
}

// Exact-version contract: https://developers.openai.com/codex/app-server/
// thread/start returns instructionSources; AGENTS.md uses a bounded instruction
// budget (https://developers.openai.com/codex/guides/agents-md/).
func probeRuleProcess(ctx context.Context, binary toolBinary, cwd, home string) (sources []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary.Path, "app-server", "--listen", "stdio://")
	c.Dir, c.Env, c.Stderr, c.WaitDelay = cwd, probeEnv(home, "codex-cli"), io.Discard, time.Second
	input, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stop, err := startProbe(c)
	if err != nil {
		return nil, err
	}
	defer func() {
		input.Close()
		cleanup := stop()
		_ = c.Wait()
		if err == nil && cleanup != nil {
			err = cleanup
		}
	}()
	send := func(v any) error { return json.NewEncoder(input).Encode(v) }
	if err = send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "aidock_rules", "version": "0.26"}, "capabilities": map[string]bool{"experimentalApi": false}}}); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(io.LimitReader(output, 8<<20))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	initialized := false
	for scanner.Scan() {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return nil, fmt.Errorf("工具返回无效 JSON")
		}
		if msg.ID != 1 && msg.ID != 2 {
			continue
		}
		if len(msg.Error) != 0 && string(msg.Error) != "null" {
			return nil, fmt.Errorf("工具拒绝本地规则协议")
		}
		if msg.ID == 1 {
			if initialized {
				return nil, fmt.Errorf("工具重复初始化响应")
			}
			initialized = true
			if err = send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
				return nil, err
			}
			if err = send(map[string]any{"id": 2, "method": "thread/start", "params": map[string]any{"cwd": cwd, "ephemeral": true, "persistExtendedHistory": false, "approvalPolicy": "never", "sandbox": "read-only", "config": map[string]any{"mcp_servers": map[string]any{}, "web_search": "disabled", "project_doc_max_bytes": ruleDocumentBudget}}}); err != nil {
				return nil, err
			}
			continue
		}
		if !initialized {
			return nil, fmt.Errorf("工具缺少初始化响应")
		}
		var r struct {
			Sources []string `json:"instructionSources"`
			CWD     string   `json:"cwd"`
			Thread  struct {
				Ephemeral bool `json:"ephemeral"`
			} `json:"thread"`
		}
		if err = json.Unmarshal(msg.Result, &r); err != nil {
			return nil, err
		}
		if !r.Thread.Ephemeral || !samePath(r.CWD, cwd) {
			return nil, fmt.Errorf("工具规则会话范围不符")
		}
		return r.Sources, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("工具在返回规则来源前退出")
}
