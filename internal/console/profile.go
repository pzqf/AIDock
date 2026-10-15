package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func (s *Server) profileAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/profile/state"
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
	m, err := s.requestEngine(r).Sources()
	if err != nil {
		operationError(w, err, "profile")
		return
	}
	var items any
	if read {
		items, err = m.Profile()
	} else if r.URL.Path == "/api/profile/apply" {
		var req struct {
			Target string `json:"target"`
			Plan   string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyProfile(r.Context(), req.Target, req.Plan, req.Digest)
		}
	} else if r.URL.Path == "/api/profile/recover-local" {
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RecoverLocalProfile()
		}
	} else {
		var req source.ProfileEdit
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ProfilePlan(r.Context(), req)
		}
	}
	if err != nil {
		operationError(w, err, "profile")
		return
	}
	v := result.New("profile", items)
	v.Summary = "共享 Profile 源管理；原生应用和会话生效另行检查"
	writeJSON(w, 200, v)
}
