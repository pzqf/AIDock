package console

import (
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) replicaAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/replica/list" || r.URL.Path == "/api/replica/rehearsal/latest" || r.URL.Path == "/api/replica/rehearsal/history" || r.URL.Path == "/api/replica/generation/check/history"
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
	m, err := s.requestEngine(r).Replicas()
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
	case "/api/replica/resolve":
		var req replica.ResolveRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Resolve(r.Context(), req)
		}
	case "/api/replica/generation":
		var req replica.GenerationRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.StageGeneration(r.Context(), req)
		}
	case "/api/replica/generation/external":
		var req replica.ExternalGenerationRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.StageExternalGeneration(r.Context(), req)
		}
	case "/api/replica/generation/repair":
		var req replica.GenerationRepairRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.StageRepair(r.Context(), req)
		}
	case "/api/replica/generation/check":
		var req replica.GenerationCheckRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.CheckGeneration(r.Context(), req)
		}
	case "/api/replica/generation/check/history":
		items, err = m.GenerationCheckHistory()
	case "/api/replica/generation/check/show":
		var req struct {
			ID string `json:"check_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.GenerationCheckShow(r.Context(), req.ID)
		}
	case "/api/replica/generation/show", "/api/replica/rehearsal/generated":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			if r.URL.Path == "/api/replica/generation/show" {
				items, err = m.GenerationShow(req.ID, req.Digest)
			} else {
				items, err = m.RehearseGenerated(r.Context(), req.ID, req.Digest)
			}
		}
	case "/api/replica/rehearsal/latest":
		items, err = m.RehearsalLatest()
	case "/api/replica/rehearsal/history":
		items, err = m.RehearsalHistory()
	case "/api/replica/rehearsal/show":
		var req struct {
			Session string `json:"session"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RehearsalShow(req.Session)
		}
	case "/api/replica/rehearsal":
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Rehearse(r.Context(), req.ID, req.Digest)
		}
	case "/api/replica/rehearsal/plan":
		var req struct {
			Session     string `json:"session"`
			Transaction string `json:"transaction"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RehearsalReverse(req.Session, req.Transaction)
		}
	case "/api/replica/rehearsal/apply":
		var req struct {
			Session string `json:"session"`
			ID      string `json:"plan_id"`
			Digest  string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RehearsalApply(r.Context(), req.Session, req.ID, req.Digest)
		}
	case "/api/replica/rehearsal/recover":
		var req struct {
			Session string `json:"session"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RehearsalRecover(req.Session)
		}
	}
	if err != nil {
		operationError(w, err, "replica")
		return
	}
	v := result.New("replica", items)
	v.Summary = "副本登记、比较及独立预演；已登记项目技能源与工具目录未修改"
	writeJSON(w, 200, v)
}
