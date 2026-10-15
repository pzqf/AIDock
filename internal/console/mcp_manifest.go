package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) mcpManifestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.workMu.Lock()
	defer s.workMu.Unlock()
	select {
	case <-s.closed:
		http.Error(w, "Console closed", http.StatusServiceUnavailable)
		return
	default:
	}
	m, err := s.requestEngine(r).Sources()
	if err != nil {
		operationError(w, err, "mcp manifest")
		return
	}
	out, err := m.MCPManifest(r.Context(), r.URL.Query().Get("name"))
	if err != nil {
		operationError(w, err, "mcp manifest")
		return
	}
	v := result.New("mcp manifest", out)
	v.Summary = "预备产物清单与直接依赖检查；尚未安装或生成正式 SBOM"
	writeJSON(w, http.StatusOK, v)
}
