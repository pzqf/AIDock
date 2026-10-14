package console

import (
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) replicaAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/replica/list"
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
	m, err := s.engine.Replicas()
	if err != nil {
		operationError(w, err, "replica")
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/replica/list":
		items, err = m.List()
	case "/api/replica/register":
		var req replica.Binding
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Register(req)
		}
	case "/api/replica/compare", "/api/replica/baseline":
		var req replica.Request
		err = decodeSource(w, r, &req)
		if err == nil {
			if r.URL.Path == "/api/replica/compare" {
				items, err = m.Compare(r.Context(), req)
			} else {
				items, err = m.BaselinePlan(r.Context(), req)
			}
		}
	case "/api/replica/confirm":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Apply(r.Context(), req.ID, req.Digest)
		}
	case "/api/replica/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Recover()
		}
	}
	if err != nil {
		operationError(w, err, "replica")
		return
	}
	v := result.New("replica", items)
	v.Summary = "仅本机副本登记/共同基线/三方比较；项目技能源与工具目录未修改"
	writeJSON(w, 200, v)
}
