package console

import (
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) projectSnapshotAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/snapshots"
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
	m, err := s.requestEngine(r).Native()
	if err != nil {
		operationError(w, err, "snapshot")
		return
	}
	var out any
	if read {
		out, err = m.ProjectSnapshots(r.Context())
	} else {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 快照核验请求", 2), "snapshot")
			return
		}
		var raw []byte
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if err == nil {
			var fields map[string]any
			fields, err = decodeConnectionControl(raw, "snapshot_id")
			if err == nil {
				id, _ := fields["snapshot_id"].(string)
				out, err = m.CheckProjectSnapshot(r.Context(), id)
			}
		}
	}
	if err != nil {
		operationError(w, err, "snapshot")
		return
	}
	v := result.New("snapshot", out)
	v.Summary = "只读查看或核验归档；完整项目 replay 写入与当前安全策略仍需独立实现和验收"
	writeJSON(w, 200, v)
}
