package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) toolEvidenceAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.URL.RawQuery != "" {
		operationError(w, result.Fail("INVALID_INPUT", "生效证据使用当前明确绑定，不接受额外查询范围", 2), "tool evidence")
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
	report, err := s.requestEngine(r).NativeEvidence(r.Context())
	if err != nil {
		operationError(w, err, "tool evidence")
		return
	}
	out := result.New("tool evidence", report)
	out.Summary = report.Boundary
	writeJSON(w, 200, out)
}
