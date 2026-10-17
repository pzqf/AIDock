package console

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
)

func (s *Server) projectSnapshotReplayAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	m, err := s.requestEngine(r).SnapshotReplayPermissions()
	if err != nil {
		operationError(w, err, "snapshot replay permission")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 回放候选审查请求", 2), "snapshot replay permission")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil || !json.Valid(raw) {
		operationError(w, result.Fail("INVALID_INPUT", "需要大小受限的单个 JSON 候选请求", 2), "snapshot replay")
		return
	}
	var out any
	if err == nil {
		switch r.URL.Path {
		case "/api/snapshots/replay/prepare":
			var req native.SnapshotReplayPreparationRequest
			req, err = native.ParseSnapshotReplayPreparationRequest(raw)
			if err == nil {
				var executable string
				executable, err = os.Executable()
				if err == nil {
					out, err = m.PrepareSnapshotReplay(r.Context(), req, executable, s.requestEngine(r).Roots.Home)
				}
			}
		case "/api/snapshots/replay/prepared":
			var fields map[string]any
			fields, err = decodeConnectionControl(raw, "id")
			if err == nil {
				id, _ := fields["id"].(string)
				out, err = m.ReadSnapshotReplayPreparation(r.Context(), id)
			}
		case "/api/snapshots/replay/review":
			var id string
			var restore *native.SnapshotReplayRestoreTarget
			id, restore, err = native.ParseSnapshotReplayReviewRequest(raw)
			if err == nil {
				out, err = m.ReviewSnapshotReplayTarget(r.Context(), id, restore)
			}
		case "/api/snapshots/replay/permission/plan":
			var req native.SnapshotReplayPermissionRequest
			req, err = native.ParseSnapshotReplayPermissionRequest(raw)
			if err == nil {
				out, err = m.PlanSnapshotReplayPermission(r.Context(), req)
			}
		case "/api/snapshots/replay/runtime/state", "/api/snapshots/replay/clients":
			var req native.SnapshotReplayCandidateRequest
			req, err = native.ParseSnapshotReplayCandidateRequest(raw)
			if err == nil {
				if r.URL.Path == "/api/snapshots/replay/runtime/state" {
					out, err = m.ReviewSnapshotReplayRuntime(r.Context(), req)
				} else {
					var executable string
					executable, err = os.Executable()
					if err == nil {
						out, err = m.SnapshotReplayClientConfigs(r.Context(), req, executable, s.requestEngine(r).Roots.Home)
					}
				}
			}
		case "/api/snapshots/replay/runtime/check":
			var req native.SnapshotReplayRuntimeRequest
			req, err = native.ParseSnapshotReplayRuntimeRequest(raw)
			if err == nil {
				out, err = m.CheckSnapshotReplayRuntime(r.Context(), req)
			}
		case "/api/snapshots/replay/permission/apply":
			var fields map[string]any
			fields, err = decodeConnectionControl(raw, "plan_id", "digest")
			if err == nil {
				id, _ := fields["plan_id"].(string)
				digest, _ := fields["digest"].(string)
				var jstate map[string]string
				j, e := m.ApplySnapshotReplayPermission(r.Context(), id, digest)
				err = e
				if e == nil {
					jstate = map[string]string{"id": j.Plan.ID, "state": j.State}
				}
				out = jstate
			}
		}
	}
	if err != nil {
		operationError(w, err, "snapshot replay permission")
		return
	}
	v := result.New("snapshot replay permission", out)
	v.Summary = "候选授权、固定运行时核验与只读连接预览；源、本机覆盖和原生工具未应用，不启动插件或读取密钥"
	if e, ok := out.(runtimecheck.Event); ok && e.Code != "RUNTIME_VERSION_MATCH" {
		v.OK = false
		v.Code = e.Code
	}
	writeJSON(w, 200, v)
}
