package console

import (
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) appliedViewAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	m, err := s.requestEngine(r).SnapshotReader()
	if err != nil {
		operationError(w, err, "tool applied")
		return
	}
	v, err := m.AppliedViews(r.Context())
	if err != nil {
		operationError(w, err, "tool applied")
		return
	}
	out := result.New("tool applied", v)
	out.Summary = v.Boundary
	writeJSON(w, 200, out)
}
