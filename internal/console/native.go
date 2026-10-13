package console

import (
	"net/http"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) nativeAPI(w http.ResponseWriter, r *http.Request) {
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
	m, err := s.engine.Native()
	if err != nil {
		operationError(w, err, "native preview")
		return
	}
	var p native.Plan
	if r.URL.Path == "/api/native/plan" {
		var req native.Request
		err = decodeSource(w, r, &req)
		if err == nil {
			p, err = m.Plan(r.Context(), req)
		}
	} else {
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			if r.URL.Path == "/api/native/apply" {
				err = m.Apply(r.Context(), req.ID, req.Digest)
			} else {
				p, err = m.Diff(r.Context(), req.ID, req.Digest)
			}
		}
	}
	if err != nil {
		operationError(w, err, "native preview")
		return
	}
	// A completed diagnostic can contain blocked targets. Return its complete
	// report so the console can render them just like the CLI's plan result.
	v := result.New("native preview", p)
	v.Summary = p.Boundary
	writeJSON(w, 200, v)
}
