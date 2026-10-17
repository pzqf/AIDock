package console

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) replayAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		operationError(w, result.Fail("INVALID_INPUT", "恢复操作需要明确 JSON 请求", 2), "replay")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil || !json.Valid(raw) {
		operationError(w, result.Fail("INVALID_INPUT", "需要受限的单个恢复 JSON 请求", 2), "replay")
		return
	}
	req, err := engine.ParseReplayRequest(raw)
	if err != nil {
		operationError(w, err, "replay")
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
	executable, err := os.Executable()
	if err != nil {
		operationError(w, err, "replay")
		return
	}
	out, err := s.requestEngine(r).Replay(r.Context(), req, executable)
	if err != nil {
		operationError(w, err, "replay")
		return
	}
	v := result.New("replay", out)
	v.Summary = out.Boundary
	// Preview may return a useful blocked report. A write request cannot report
	// success unless its owning transaction was actually committed.
	if req.Action == "apply" && out.Action != "committed" {
		v.OK, v.Code = false, out.Code
		if v.Code == "" {
			v.Code = "REPLAY_NOT_COMMITTED"
		}
	}
	writeJSON(w, 200, v)
}
