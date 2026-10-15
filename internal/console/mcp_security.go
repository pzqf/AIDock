package console

import (
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) mcpSecurityAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/security/state"
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
		operationError(w, err, "mcp security")
		return
	}
	var out any
	switch r.URL.Path {
	case "/api/mcp/security/state":
		out, err = m.MCPSecurity(r.Context())
	case "/api/mcp/security/plan":
		var req pluginsecurity.Edit
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.MCPSecurityPlan(r.Context(), req)
		}
	case "/api/mcp/security/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.ApplyMCPSecurity(r.Context(), req.ID, req.Digest)
		}
	case "/api/mcp/security/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			n, e := m.RecoverMCPSecurity(r.Context())
			err = e
			out = map[string]int{"recovered": n}
		}
	}
	if err != nil {
		operationError(w, err, "mcp security")
		return
	}
	writeJSON(w, 200, result.New("mcp security", out))
}
