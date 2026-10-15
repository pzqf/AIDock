package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) mcpOriginAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/origin/state" || r.URL.Path == "/api/mcp/origin/show"
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
		operationError(w, err, "mcp origin")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/mcp/origin/state":
		items, err = m.MCPOrigin(r.Context())
	case "/api/mcp/origin/show":
		var document string
		document, err = m.ShowMCPOrigin(r.Context(), r.URL.Query().Get("subject"))
		items = map[string]string{"document": document}
	case "/api/mcp/origin/plan":
		var req pluginorigin.Edit
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.MCPOriginPlan(r.Context(), req)
		}
	case "/api/mcp/origin/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyMCPOrigin(r.Context(), req.ID, req.Digest)
		}
	case "/api/mcp/origin/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPOrigin(r.Context())
			items = map[string]int{"recovered": n}
		}
	}
	if err != nil {
		operationError(w, err, "mcp origin")
		return
	}
	v := result.New("mcp origin", items)
	v.Summary = "当前项目本机来源签名证据；安全评估、运行授权与隔离另行判断"
	writeJSON(w, 200, v)
}
