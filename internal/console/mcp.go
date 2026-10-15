package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func (s *Server) mcpAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/state"
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
	m, err := s.requestEngine(r).Sources()
	if err != nil {
		operationError(w, err, "mcp")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/mcp/state":
		items, err = m.MCP()
	case "/api/mcp/plan":
		var req source.MCPEdit
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.MCPPlan(r.Context(), req)
		}
	case "/api/mcp/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyMCP(r.Context(), req.ID, req.Digest)
		}
	}
	if err != nil {
		operationError(w, err, "mcp")
		return
	}
	v := result.New("mcp", items)
	v.Summary = "MCP 定义管理；安装、授权和受管运行分别检查"
	writeJSON(w, 200, v)
}
