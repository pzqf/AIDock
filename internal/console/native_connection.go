package console

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
)

func decodeConnectionControl(raw []byte, allowed ...string) (map[string]any, error) {
	bad := func() (map[string]any, error) {
		return nil, result.Fail("INVALID_INPUT", "需要字段名称与类型明确的单个 JSON 请求；不接受重复、别名、空值或自定义命令", 2)
	}
	if !json.Valid(raw) {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for name, value := range fields {
		found := false
		for _, key := range allowed {
			found = found || key == name
		}
		if !found {
			return bad()
		}
		if name == "tools" {
			tools, ok := value.([]any)
			if !ok || len(tools) == 0 || len(tools) > 2 {
				return bad()
			}
			for _, tool := range tools {
				if _, ok := tool.(string); !ok {
					return bad()
				}
			}
		} else if _, ok := value.(string); !ok {
			return bad()
		}
	}
	return fields, nil
}

func (s *Server) nativeConnectionAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/native/mcp/history" || r.URL.Path == "/api/native/mcp/versions"
	if read && r.Method != http.MethodGet || !read && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	s.workMu.Lock()
	defer s.workMu.Unlock()
	select {
	case <-s.closed:
		http.Error(w, "Console closed", 503)
		return
	default:
	}
	e := s.requestEngine(r)
	m, err := e.Native()
	if err != nil {
		operationError(w, err, "tool mcp")
		return
	}
	var raw []byte
	if !read {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 请求", 2), "tool mcp")
			return
		}
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
		if err != nil || !json.Valid(raw) {
			operationError(w, result.Fail("INVALID_INPUT", "需要大小受限的单个 JSON 对象", 2), "tool mcp")
			return
		}
	}
	var out any
	if r.URL.Path == "/api/native/mcp/plan" {
		var req native.MCPConnectionRequest
		req, err = native.ParseMCPConnectionRequest(raw)
		if err == nil {
			var executable string
			executable, err = os.Executable()
			if err == nil {
				out, err = m.MCPConnectionPlan(r.Context(), req, executable, e.Roots.Home)
			}
		}
	} else if r.URL.Path == "/api/native/mcp/history" {
		out, err = m.MCPConnectionHistory()
	} else if r.URL.Path == "/api/native/mcp/versions" {
		out, err = m.LatestMCPConnectionVersions()
	} else {
		var fields map[string]any
		switch r.URL.Path {
		case "/api/native/mcp/versions/check":
			fields, err = decodeConnectionControl(raw, "tools")
		case "/api/native/mcp/apply", "/api/native/mcp/rollback/apply":
			fields, err = decodeConnectionControl(raw, "plan_id", "digest")
		case "/api/native/mcp/rollback/plan":
			fields, err = decodeConnectionControl(raw, "transaction")
		case "/api/native/mcp/recover":
			fields, err = decodeConnectionControl(raw)
		}
		if err == nil {
			text := func(key string) string { value, _ := fields[key].(string); return value }
			switch r.URL.Path {
			case "/api/native/mcp/versions/check":
				tools := []string{}
				if list, ok := fields["tools"].([]any); ok {
					for _, value := range list {
						tools = append(tools, value.(string))
					}
				}
				out, err = m.CheckMCPConnectionVersions(r.Context(), tools)
			case "/api/native/mcp/apply":
				out, err = m.ApplyMCPConnection(r.Context(), text("plan_id"), text("digest"))
			case "/api/native/mcp/rollback/plan":
				out, err = m.ReverseMCPConnection(r.Context(), text("transaction"))
			case "/api/native/mcp/rollback/apply":
				out, err = m.ApplyMCPConnectionReverse(r.Context(), text("plan_id"), text("digest"))
			case "/api/native/mcp/recover":
				var count int
				count, err = m.RecoverMCPConnections()
				out = map[string]int{"recovered": count}
			}
		}
	}
	if err != nil {
		operationError(w, err, "tool mcp")
		return
	}
	v := result.New("tool mcp", out)
	v.Summary = "原生 MCP 连接使用独立预览和恢复事务；安装不启动插件，原始字节仅备份到受限本机目录"
	writeJSON(w, 200, v)
}
