package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) mcpPermissionAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/permission/state"
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
		operationError(w, err, "mcp permission")
		return
	}
	var out any
	switch r.URL.Path {
	case "/api/mcp/permission/state":
		out, err = m.MCPPermission(r.Context())
	case "/api/mcp/permission/plan":
		var req pluginpermission.Edit
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.MCPPermissionPlan(r.Context(), req)
		}
	case "/api/mcp/permission/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.ApplyMCPPermission(r.Context(), req.ID, req.Digest)
		}
	case "/api/mcp/permission/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPPermission(r.Context())
			out = map[string]int{"recovered": n}
		}
	}
	if err != nil {
		operationError(w, err, "mcp permission")
		return
	}
	v := result.New("mcp permission", out)
	v.Summary = "项目副本权限决策与审计；隔离、逐次调用授权和受管启动独立判断"
	writeJSON(w, 200, v)
}
