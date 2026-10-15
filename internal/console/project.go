package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/project"
	"github.com/pzqf/AIDock/internal/result"
)

type projectEngineKey struct{}

func (s *Server) requestEngine(r *http.Request) *engine.Engine {
	if e, ok := r.Context().Value(projectEngineKey{}).(*engine.Engine); ok {
		return e
	}
	return s.engine
}
func (s *Server) bindRequest(r *http.Request) (*http.Request, error) {
	id := r.Header.Get("X-AIDock-Binding")
	if id == "" || strings.HasPrefix(r.URL.Path, "/api/projects/") || r.URL.Path == "/api/events" || r.URL.Path == "/api/close" {
		return r, nil
	}
	e, err := s.engine.ProjectEngine(id)
	if err != nil {
		return r, err
	}
	return r.WithContext(context.WithValue(r.Context(), projectEngineKey{}, e)), nil
}
func (s *Server) projectAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/projects/list" || r.URL.Path == "/api/projects/history"
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
	m, err := s.engine.Projects()
	if err != nil {
		operationError(w, err, "project")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/projects/list":
		items, err = m.List()
	case "/api/projects/history":
		items, err = m.History()
	case "/api/projects/plan":
		var req project.Request
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Plan(r.Context(), req)
		}
	case "/api/projects/apply", "/api/projects/recover":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest,omitempty"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			if r.URL.Path == "/api/projects/apply" {
				items, err = m.Apply(r.Context(), req.ID, req.Digest)
			} else if req.Digest != "" {
				err = result.Fail("INVALID_INPUT", "恢复不接受替代摘要", 2)
			} else {
				items, err = m.Recover(req.ID)
			}
		}
	}
	if err != nil {
		operationError(w, err, "project")
		return
	}
	v := result.New("project", items)
	v.Summary = "项目目录与身份已核对；工具应用另行预览"
	writeJSON(w, 200, v)
}
