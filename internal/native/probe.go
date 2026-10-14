package native

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pzqf/AIDock/internal/workstore"
)

// These protocols are restricted to locally observed exact versions. A version
// outside this list is UNKNOWN; we don't try arbitrary CLI startup flags.
type toolBinary struct{ Path, Digest string }
type discoveredSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
}
type discovery struct {
	Skills []discoveredSkill
	Errors int
}

func probeFailure(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "超过本地检查的等待时间"
	}
	if errors.Is(err, context.Canceled) {
		return "检查已取消"
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return "工具协议字段类型不兼容：" + typeError.Field
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return "工具协议内容不是有效 JSON"
	}
	// Protocol failures constructed here contain no captured stderr or tool
	// payloads, credentials, home contents or arbitrary skill descriptions.
	for _, text := range []string{"工具返回无效 JSON", "工具拒绝本地发现协议", "工具未返回指定 cwd 的发现结果", "工具拒绝初始化协议", "初始化缺少命令列表", "工具在返回发现结果前退出"} {
		if err.Error() == text {
			return text
		}
	}
	return "本地进程或协议读取失败"
}

func resolveBinary(id string) (toolBinary, error) {
	command := "codex"
	if id == "claude-code" {
		command = "claude"
	}
	p, err := exec.LookPath(command)
	if err != nil {
		return toolBinary{}, err
	}
	// Windows npm shims are shell programs. Use the packaged native binary;
	// never invoke a shell or parse/interpolate an arbitrary shim as commands.
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(p), ".exe") {
		if id != "codex-cli" {
			return toolBinary{}, fmt.Errorf("不支持此工具的 shell 包装器")
		}
		arch, triple := "x64", "x86_64-pc-windows-msvc"
		if runtime.GOARCH == "arm64" {
			arch, triple = "arm64", "aarch64-pc-windows-msvc"
		}
		pkg := filepath.Join(filepath.Dir(p), "node_modules", "@openai", "codex")
		candidates := []string{
			filepath.Join(pkg, "node_modules", "@openai", "codex-win32-"+arch, "vendor", triple, "bin", "codex.exe"),
			filepath.Join(pkg, "vendor", triple, "bin", "codex.exe"),
		}
		p = ""
		for _, candidate := range candidates {
			if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
				p = candidate
				break
			}
		}
		if p == "" {
			return toolBinary{}, fmt.Errorf("未找到 npm 包的原生 codex.exe")
		}
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return toolBinary{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		return toolBinary{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return toolBinary{}, err
	}
	return toolBinary{p, hex.EncodeToString(h.Sum(nil))}, nil
}

// Keep only basic OS runtime variables. Credentials, inherited tool switches,
// project settings, agents, plugins and MCP startup configuration don't travel
// into the disposable discovery process.
func probeEnv(home string, id string) []string {
	keep := map[string]bool{}
	for _, key := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL"} {
		keep[key] = true
	}
	env := []string{}
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if keep[strings.ToUpper(key)] {
			env = append(env, pair)
		}
	}
	// Child-only home directories; the caller's environment and settings remain
	// untouched. Both tools may initialize their own caches here.
	env = append(env, "HOME="+home, "USERPROFILE="+home)
	if id == "codex-cli" {
		return append(env, "CODEX_HOME="+home)
	}
	return append(env, "CLAUDE_CONFIG_DIR="+home, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
}

type cappedOutput struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(p) > b.limit {
		return 0, fmt.Errorf("工具输出超过限制")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func toolVersion(ctx context.Context, binary toolBinary, id, cwd, home string) (version string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary.Path, "--version")
	c.WaitDelay = time.Second
	c.Dir, c.Env = cwd, probeEnv(home, id)
	out := &cappedOutput{limit: 4096}
	c.Stdout, c.Stderr = out, io.Discard
	stop, err := startProbe(c)
	if err != nil {
		return "", err
	}
	defer func() {
		if cleanup := stop(); err == nil && cleanup != nil {
			err = cleanup
		}
	}()
	if err := c.Wait(); err != nil {
		return "", err
	}
	fields := strings.Fields(string(out.data))
	for _, field := range fields {
		if versionPattern.MatchString(field) {
			return field, nil
		}
	}
	return "", fmt.Errorf("工具没有返回可识别的精确版本")
}

func probeProcess(ctx context.Context, binary toolBinary, id, cwd, home string) (observed discovery, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"app-server", "--listen", "stdio://"}
	if id == "claude-code" {
		args = []string{"--setting-sources", "project", "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--strict-mcp-config", "--tools", "", "--settings", `{"disableAllHooks":true}`}
	}
	c := exec.CommandContext(ctx, binary.Path, args...)
	c.Dir, c.Env, c.Stderr = cwd, probeEnv(home, id), io.Discard
	c.WaitDelay = time.Second
	input, err := c.StdinPipe()
	if err != nil {
		return discovery{}, err
	}
	output, err := c.StdoutPipe()
	if err != nil {
		return discovery{}, err
	}
	stop, err := startProbe(c)
	if err != nil {
		return discovery{}, err
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
	if id == "codex-cli" {
		err = send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "aidock_discovery", "version": "0.9"}, "capabilities": map[string]bool{"experimentalApi": false}}})
	} else {
		err = send(map[string]any{"type": "control_request", "request_id": "aidock-init", "request": map[string]string{"subtype": "initialize"}})
	}
	if err != nil {
		return discovery{}, err
	}
	scanner := bufio.NewScanner(io.LimitReader(output, 8<<20))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if id == "codex-cli" {
			var msg struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err = json.Unmarshal(line, &msg); err != nil {
				return discovery{}, fmt.Errorf("工具返回无效 JSON")
			}
			if msg.ID != 1 && msg.ID != 2 {
				continue
			}
			if len(msg.Error) != 0 && string(msg.Error) != "null" {
				return discovery{}, fmt.Errorf("工具拒绝本地发现协议")
			}
			if msg.ID == 1 {
				if err = send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
					return discovery{}, err
				}
				if err = send(map[string]any{"id": 2, "method": "skills/list", "params": map[string]any{"cwds": []string{cwd}, "forceReload": true}}); err != nil {
					return discovery{}, err
				}
				continue
			}
			var response struct {
				Data []struct {
					CWD    string            `json:"cwd"`
					Skills []discoveredSkill `json:"skills"`
					Errors []json.RawMessage `json:"errors"`
				} `json:"data"`
			}
			if err = json.Unmarshal(msg.Result, &response); err != nil {
				return discovery{}, err
			}
			for _, data := range response.Data {
				if samePath(data.CWD, cwd) {
					return discovery{data.Skills, len(data.Errors)}, nil
				}
			}
			return discovery{}, fmt.Errorf("工具未返回指定 cwd 的发现结果")
		} else {
			var msg struct {
				Type     string `json:"type"`
				Response struct {
					Subtype  string `json:"subtype"`
					ID       string `json:"request_id"`
					Response struct {
						Commands []struct {
							Name        string `json:"name"`
							Description string `json:"description"`
						} `json:"commands"`
					} `json:"response"`
				} `json:"response"`
			}
			if err = json.Unmarshal(line, &msg); err != nil {
				return discovery{}, fmt.Errorf("工具返回无效 JSON")
			}
			if msg.Type != "control_response" || msg.Response.ID != "aidock-init" {
				continue
			}
			if msg.Response.Subtype != "success" {
				return discovery{}, fmt.Errorf("工具拒绝初始化协议")
			}
			if msg.Response.Response.Commands == nil {
				return discovery{}, fmt.Errorf("初始化缺少命令列表")
			}
			d := discovery{Skills: []discoveredSkill{}}
			for _, command := range msg.Response.Response.Commands {
				d.Skills = append(d.Skills, discoveredSkill{Name: command.Name, Description: command.Description, Enabled: true})
			}
			return d, nil
		}
	}
	if ctx.Err() != nil {
		return discovery{}, ctx.Err()
	}
	if err = scanner.Err(); err != nil {
		return discovery{}, err
	}
	return discovery{}, fmt.Errorf("工具在返回发现结果前退出")
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (m *Manager) materializeProbe(project string, target Target) error {
	root := filepath.Join(project, filepath.FromSlash(target.Adapter.Directory))
	for _, pkg := range target.Packages {
		dir := filepath.Join(root, pkg.Name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for path, entry := range pkg.Tree.Entries {
			if err := workstore.SafeRelative(path); err != nil {
				return err
			}
			full := filepath.Join(dir, filepath.FromSlash(path))
			if entry.Dir {
				if err := os.MkdirAll(full, os.FileMode(entry.Mode)); err != nil {
					return err
				}
				continue
			}
			bytes, err := m.Source.Store.Data(entry)
			if err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				return err
			}
			if err = os.WriteFile(full, bytes, os.FileMode(entry.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}
