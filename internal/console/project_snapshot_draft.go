package console

import (
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) projectSnapshotDraftAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/snapshots/draft/history" || r.URL.Path == "/api/snapshots/draft/inspect"
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
	m, err := s.requestEngine(r).SnapshotDraftRecovery()
	if err != nil {
		operationError(w, err, "snapshot draft")
		return
	}
	var out any
	if read {
		if r.URL.Path == "/api/snapshots/draft/history" {
			out, err = m.SnapshotDraftHistory()
		} else {
			out, err = m.InspectSnapshotDraftRecovery()
		}
	} else {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 草稿恢复请求", 2), "snapshot draft")
			return
		}
		var raw []byte
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if err == nil {
			keys := []string{}
			switch r.URL.Path {
			case "/api/snapshots/draft/plan":
				keys = []string{"snapshot_id"}
			case "/api/snapshots/draft/apply":
				keys = []string{"plan_id", "digest"}
			case "/api/snapshots/draft/reverse":
				keys = []string{"transaction"}
			}
			var fields map[string]any
			fields, err = decodeConnectionControl(raw, keys...)
			if err == nil {
				text := func(key string) string { v, _ := fields[key].(string); return v }
				switch r.URL.Path {
				case "/api/snapshots/draft/plan":
					out, err = m.PrepareSnapshotDraft(r.Context(), text("snapshot_id"))
				case "/api/snapshots/draft/apply":
					j, e := m.ApplySnapshotDraft(r.Context(), text("plan_id"), text("digest"))
					err = e
					out = map[string]string{"id": j.Plan.ID, "state": j.State, "action": j.Plan.Action}
				case "/api/snapshots/draft/reverse":
					out, err = m.ReverseSnapshotDraft(r.Context(), text("transaction"))
				case "/api/snapshots/draft/recover":
					n, e := m.RecoverSnapshotDraft()
					err = e
					out = map[string]int{"recovered": n}
				}
			}
		}
	}
	if err != nil {
		operationError(w, err, "snapshot draft")
		return
	}
	v := result.New("snapshot draft", out)
	v.Summary = "仅管理已保存草稿的两层共同恢复；未应用原生工具，未恢复安装、授权或凭据"
	writeJSON(w, 200, v)
}
