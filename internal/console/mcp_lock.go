package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func (s *Server) mcpLockAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/lock/state"
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
		operationError(w, err, "mcp lock")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/mcp/lock/state":
		items, err = m.MCPLock(r.Context())
	case "/api/mcp/lock/inspect":
		var req pluginlock.Preparation
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.InspectMCPLock(r.Context(), req)
		}
	case "/api/mcp/lock/plan":
		var req source.MCPLockEdit
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.MCPLockPlan(r.Context(), req)
		}
	case "/api/mcp/lock/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyMCPLock(r.Context(), req.ID, req.Digest)
		}
	}
	if err != nil {
		operationError(w, err, "mcp lock")
		return
	}
	v := result.New("mcp lock", items)
	v.Summary = "安装锁与本地产物核验；无安装、授权或运行"
	writeJSON(w, 200, v)
}
