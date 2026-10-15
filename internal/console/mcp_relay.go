package console

import (
	"encoding/json"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) mcpRelayAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var raw json.RawMessage
	if err := decodeSource(w, r, &raw); err != nil {
		operationError(w, err, "mcp relay-check")
		return
	}
	probe, err := mcprelay.ParseCheckRequest(raw)
	if err != nil {
		operationError(w, result.Fail("INVALID_INPUT", "只接受明确的 probe 布尔字段", 2), "mcp relay-check")
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
	report, err := mcprelay.Check(r.Context(), probe)
	if err != nil {
		operationError(w, err, "mcp relay-check")
		return
	}
	v := result.New("mcp relay-check", report)
	v.Summary = report.Boundary
	if report.Status == "FAIL" {
		v.Code = "STATIC_CHECK_FAILED"
	}
	writeJSON(w, 200, v)
}
