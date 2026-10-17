package console

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) projectSnapshotReplayControlAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/snapshots/replay/history" || r.URL.Path == "/api/snapshots/replay/inspect"
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
	e := s.requestEngine(r)
	m, err := e.SnapshotReader()
	if err != nil {
		operationError(w, err, "snapshot replay")
		return
	}
	var out any
	if read {
		if r.URL.Path == "/api/snapshots/replay/history" {
			out, err = m.SnapshotReplayHistory()
		} else {
			out, err = m.InspectSnapshotReplayRecovery()
		}
	} else {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 共同回放请求", 2), "snapshot replay")
			return
		}
		raw, er := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if er != nil || !json.Valid(raw) {
			operationError(w, result.Fail("INVALID_INPUT", "需要受限的单个 JSON 请求", 2), "snapshot replay")
			return
		}
		keys := []string{}
		switch r.URL.Path {
		case "/api/snapshots/replay/plan":
			keys = []string{"prepared_id", "digest"}
		case "/api/snapshots/replay/planned", "/api/snapshots/replay/apply":
			keys = []string{"plan_id", "digest"}
		case "/api/snapshots/replay/reverse":
			keys = []string{"transaction"}
		}
		fields, er := decodeConnectionControl(raw, keys...)
		err = er
		if err == nil {
			text := func(key string) string { v, _ := fields[key].(string); return v }
			switch r.URL.Path {
			case "/api/snapshots/replay/plan":
				exe, er := os.Executable()
				err = er
				if er == nil {
					out, err = m.PlanSnapshotReplay(r.Context(), text("prepared_id"), text("digest"), exe, e.Roots.Home)
				}
			case "/api/snapshots/replay/planned":
				out, err = m.ReadSnapshotReplayPlan(r.Context(), text("plan_id"), text("digest"))
			case "/api/snapshots/replay/apply":
				out, err = m.ApplySnapshotReplay(r.Context(), text("plan_id"), text("digest"))
			case "/api/snapshots/replay/reverse":
				out, err = m.ReverseSnapshotReplay(r.Context(), text("transaction"))
			case "/api/snapshots/replay/recover":
				var n int
				n, err = m.RecoverSnapshotReplay()
				out = map[string]int{"recovered": n}
			}
		}
	}
	if err != nil {
		operationError(w, err, "snapshot replay")
		return
	}
	v := result.New("snapshot replay", out)
	v.Summary = "共同回放观察与确认；can_apply 仅是本次观察，执行仍重新核验全部输入与当前权限"
	writeJSON(w, 200, v)
}
