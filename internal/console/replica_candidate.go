package console

import (
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
)

// Called inside replicaAPI's serialized work section after request binding.
func replicaCandidateAPI(w http.ResponseWriter, r *http.Request, m *replica.Manager) (items any, err error) {
	switch r.URL.Path {
	case "/api/replica/candidate/show":
		var req replica.CandidateRequest
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			items, err = m.Candidate(r.Context(), req)
		}
	case "/api/replica/candidate/artifact-plan":
		var req replica.CandidateArtifactRequest
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			items, err = m.CandidateArtifactPlan(r.Context(), req.CandidateRequest, req.Edit)
		}
	case "/api/replica/candidate/install-plan":
		var req replica.CandidateInstallRequest
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			items, err = m.CandidateInstallPlan(r.Context(), req.CandidateRequest, req.Edit)
		}
	case "/api/replica/candidate/permission-plan":
		var req replica.CandidatePermissionRequest
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			items, err = m.CandidatePermissionPlan(r.Context(), req.CandidateRequest, req.Edit)
		}
	case "/api/replica/candidate/runtime-check":
		var req replica.CandidateRuntimeRequest
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			items, err = m.CandidateRuntimeCheck(r.Context(), req.CandidateRequest, req.Check)
		}
	default:
		var req replica.CandidateConfirmation
		if err = decodeReplicaCandidate(w, r, &req); err == nil {
			switch r.URL.Path {
			case "/api/replica/candidate/artifact-confirm":
				items, err = m.CandidateArtifactApply(r.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
			case "/api/replica/candidate/install-confirm":
				items, err = m.CandidateInstallApply(r.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
			case "/api/replica/candidate/permission-confirm":
				items, err = m.CandidatePermissionApply(r.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
			}
		}
	}
	return items, err
}

func decodeReplicaCandidate(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return result.Fail("INVALID_INPUT", "需要 JSON 候选请求", 2)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		return result.Fail("INVALID_INPUT", "候选请求过大或无法完整读取", 2)
	}
	return replica.ParseCandidateInput(raw, target)
}
