package console

import (
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
	"strings"
)

func (s *Server) replicaAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/replica/list" || r.URL.Path == "/api/replica/rehearsal/latest" || r.URL.Path == "/api/replica/rehearsal/history" || r.URL.Path == "/api/replica/generation/check/history" || r.URL.Path == "/api/replica/publication/history"
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
	case "/api/replica/candidate/show", "/api/replica/candidate/artifact-plan", "/api/replica/candidate/artifact-confirm", "/api/replica/candidate/install-plan", "/api/replica/candidate/install-confirm", "/api/replica/candidate/permission-plan", "/api/replica/candidate/permission-confirm", "/api/replica/candidate/runtime-check":
		items, err = replicaCandidateAPI(w, r, m)
	case "/api/replica/publication/plan":
		var req replica.PublicationRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.PlanPublication(r.Context(), req)
		}
	case "/api/replica/publication/history":
		items, err = m.PublicationHistory()
	case "/api/replica/publication/apply", "/api/replica/publication/show", "/api/replica/publication/reverse", "/api/replica/publication/apply-reverse", "/api/replica/publication/recover", "/api/replica/publication/inspect-recovery":
		var req struct {
			ID          string `json:"publication_id"`
			Digest      string `json:"digest"`
			Transaction string `json:"transaction"`
			Plan        string `json:"plan_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			switch r.URL.Path {
			case "/api/replica/publication/apply":
				items, err = m.ApplyPublication(r.Context(), req.ID, req.Digest)
			case "/api/replica/publication/show":
				items, err = m.PublicationShow(req.ID)
			case "/api/replica/publication/reverse":
				items, err = m.ReversePublication(req.ID, req.Transaction)
			case "/api/replica/publication/apply-reverse":
				items, err = m.ApplyPublicationReverse(r.Context(), req.ID, req.Plan, req.Digest)
			case "/api/replica/publication/recover":
				items, err = m.RecoverPublication(req.ID)
			case "/api/replica/publication/inspect-recovery":
				items, err = m.InspectPublicationRecovery(req.ID)
			}
		}
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
	case "/api/replica/generation/reopen":
		var req struct {
			ID string `json:"check_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ReopenGeneration(r.Context(), req.ID)
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
	if strings.HasPrefix(r.URL.Path, "/api/replica/publication/") {
		v.Summary = "正式同步使用独立确认与整批恢复记录；发布者只读，不运行技能或工具"
	}
	writeJSON(w, 200, v)
}
