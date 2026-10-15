package native

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// The Agent SDK's get_context_usage summary is a local estimate, unlike the
// default full mode which may use the token-count API. Never send a user turn.
// Contract: @anthropic-ai/claude-agent-sdk SDKControlGetContextUsageRequest.
func probeClaudeRuleProcess(ctx context.Context, binary toolBinary, cwd, home string) (sources []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary.Path, "--setting-sources", "project", "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--strict-mcp-config", "--tools", "", "--settings", `{"disableAllHooks":true}`)
	c.Dir, c.Env, c.Stderr, c.WaitDelay = cwd, probeEnv(home, "claude-code"), io.Discard, time.Second
	// This protocol must work without an API. Keep any accidental token-count
	// fallback away from the remote endpoint, with no inherited credentials.
	c.Env = append(c.Env, "ANTHROPIC_BASE_URL=http://127.0.0.1:1")
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
	sources, err = claudeRuleProtocol(input, output)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return sources, err
}

func claudeRuleProtocol(input io.Writer, output io.Reader) (sources []string, err error) {
	send := func(id string, request map[string]string) error {
		return json.NewEncoder(input).Encode(map[string]any{"type": "control_request", "request_id": id, "request": request})
	}
	if err = send("aidock-rule-init", map[string]string{"subtype": "initialize"}); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(io.LimitReader(output, 8<<20))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	initialized := false
	for scanner.Scan() {
		var msg struct {
			Type     string `json:"type"`
			Response struct {
				Subtype  string          `json:"subtype"`
				ID       string          `json:"request_id"`
				Response json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return nil, fmt.Errorf("工具返回无效 JSON")
		}
		if msg.Type != "control_response" || msg.Response.ID != "aidock-rule-init" && msg.Response.ID != "aidock-rule-context" {
			continue
		}
		if msg.Response.Subtype != "success" {
			return nil, fmt.Errorf("工具拒绝本地规则协议")
		}
		if msg.Response.ID == "aidock-rule-init" {
			if initialized {
				return nil, fmt.Errorf("工具重复初始化响应")
			}
			initialized = true
			if err = send("aidock-rule-context", map[string]string{"subtype": "get_context_usage", "detail": "summary"}); err != nil {
				return nil, err
			}
			continue
		}
		if !initialized {
			return nil, fmt.Errorf("工具缺少初始化响应")
		}
		var response struct {
			MemoryFiles *[]struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"memoryFiles"`
		}
		if err = json.Unmarshal(msg.Response.Response, &response); err != nil {
			return nil, err
		}
		if response.MemoryFiles == nil {
			return nil, fmt.Errorf("工具未返回规则来源列表")
		}
		for _, file := range *response.MemoryFiles {
			if file.Type != "Project" {
				return nil, fmt.Errorf("工具返回候选外规则来源")
			}
			sources = append(sources, file.Path)
		}
		return sources, nil
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("工具在返回规则来源前退出")
}
