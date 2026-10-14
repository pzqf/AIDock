package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/isolation"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) isolationAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/platform/check/latest" || r.URL.Path == "/api/platform/check/pending"
	if read && r.Method != http.MethodGet || !read && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if !read {
		s.workMu.Lock()
		defer s.workMu.Unlock()
		select {
		case <-s.closed:
			http.Error(w, "Console closed", 503)
			return
		default:
		}
	}
	m, err := s.engine.Isolation()
	if err != nil {
		operationError(w, err, "platform check")
		return
	}
	var report *isolation.Report
	switch r.URL.Path {
	case "/api/platform/check/pending":
		pending, err := m.Pending()
		if err != nil {
			operationError(w, err, "platform pending")
			return
		}
		writeJSON(w, 200, result.New("platform pending", pending))
		return
	case "/api/platform/check/recover":
		var req struct{}
		if err = decodeSource(w, r, &req); err != nil {
			operationError(w, err, "platform recover")
			return
		}
		count, err := m.Recover()
		if err != nil {
			operationError(w, err, "platform recover")
			return
		}
		writeJSON(w, 200, result.New("platform recover", map[string]int{"cleaned": count}))
		return
	case "/api/platform/check/latest":
		report, err = m.Latest()
	case "/api/platform/check":
		var req struct {
			Probe bool `json:"probe"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			var v isolation.Report
			v, err = m.Check(r.Context(), req.Probe)
			report = &v
		}
	case "/api/platform/check/show":
		var req struct {
			ID string `json:"check_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			var v isolation.Report
			v, err = m.Show(req.ID)
			report = &v
		}
	}
	if err != nil {
		operationError(w, err, "platform check")
		return
	}
	// Diagnostic envelopes stay readable even when execution gates are closed.
	v := result.New("platform check", report)
	v.Summary = "平台检查证据；外部生成器执行未开放"
	if report != nil && report.Stale {
		v.Code = "CHECK_STALE"
	} else if report != nil && report.Status == "FAIL" {
		v.Code = "STATIC_CHECK_FAILED"
	} else if report == nil || report.Status != "PASS" {
		v.Code = "EVIDENCE_INCOMPLETE"
	}
	writeJSON(w, 200, v)
}
