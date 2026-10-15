package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) mcpArtifactAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/artifact/state"
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
		operationError(w, err, "mcp artifact")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/mcp/artifact/state":
		items, err = m.MCPArtifacts(r.Context())
	case "/api/mcp/artifact/plan":
		var req pluginartifact.Edit
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.MCPArtifactPlan(r.Context(), req)
		}
	case "/api/mcp/artifact/apply":
		var req struct {
			Name   string `json:"name"`
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyMCPArtifact(r.Context(), req.Name, req.ID, req.Digest)
		}
	case "/api/mcp/artifact/recover":
		var req struct {
			Path string `json:"artifact_path"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPArtifact(r.Context(), req.Path)
			items = map[string]int{"recovered": n}
		}
	}
	if err != nil {
		operationError(w, err, "mcp artifact")
		return
	}
	v := result.New("mcp artifact", items)
	v.Summary = "完整安装包按锁导入与恢复；不运行代码或授予权限"
	writeJSON(w, 200, v)
}
