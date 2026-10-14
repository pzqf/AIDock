package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) generatorAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/generator/history"
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
	m, err := s.engine.Generators()
	if err != nil {
		operationError(w, err, "generator")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/generator/history":
		items, err = m.History(r.Context())
	case "/api/generator/inspect":
		var req struct {
			Adapter string `json:"adapter_path"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Inspect(r.Context(), req.Adapter)
		}
	case "/api/generator/stage":
		var req struct {
			Adapter string `json:"adapter_path"`
			Digest  string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Stage(r.Context(), req.Adapter, req.Digest)
		}
	case "/api/generator/show":
		var req struct {
			ID string `json:"stage_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Show(r.Context(), req.ID)
		}
	case "/api/generator/validate", "/api/generator/clean":
		var req struct {
			ID     string `json:"stage_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			if r.URL.Path == "/api/generator/clean" {
				items, err = m.Cleanup(r.Context(), req.ID, req.Digest)
			} else {
				v, validationErr := m.Validate(r.Context(), req.ID, req.Digest)
				items, err = v, validationErr
				if err == nil {
					out := result.New("generator validate", items)
					out.Summary = "离线返回包校验；不证明程序执行或工具生效"
					if v.Status != "PASS" {
						out.Code = "STATIC_CHECK_FAILED"
					}
					writeJSON(w, 200, out)
					return
				}
			}
		}
	}
	if err != nil {
		operationError(w, err, "generator")
		return
	}
	v := result.New("generator", items)
	v.Summary = "离线交换；外部执行与正式发布仍关闭"
	writeJSON(w, 200, v)
}
