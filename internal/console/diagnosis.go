package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) diagnosisAPI(w http.ResponseWriter, r *http.Request) {
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
	report, err := s.requestEngine(r).Diagnose(r.Context())
	if err != nil {
		operationError(w, err, "doctor")
		return
	}
	v := result.New("doctor", report)
	v.Summary = "项目诊断完成；当前文件与已有生效证据分别展示"
	writeJSON(w, http.StatusOK, v)
}
