package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) mcpInstallAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/install/state" || r.URL.Path == "/api/mcp/install/sbom"
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
		operationError(w, err, "mcp install")
		return
	}
	var out any
	switch r.URL.Path {
	case "/api/mcp/install/state":
		out, err = m.MCPInstall(r.Context())
	case "/api/mcp/install/sbom":
		out, err = m.MCPInstalledSBOM(r.Context(), r.URL.Query().Get("name"))
	case "/api/mcp/install/plan":
		var req plugininstall.Edit
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.MCPInstallPlan(r.Context(), req)
		}
	case "/api/mcp/install/apply":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			out, err = m.ApplyMCPInstall(r.Context(), req.ID, req.Digest)
		}
	case "/api/mcp/install/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPInstall(r.Context())
			out = map[string]int{"recovered": n}
		}
	}
	if err != nil {
		operationError(w, err, "mcp install")
		return
	}
	v := result.New("mcp install", out)
	v.Summary = "本机安装登记与直接声明 SBOM；授权、隔离和运行资格独立判断"
	writeJSON(w, 200, v)
}
