package console

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) replicaFullPublicationAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	raw, err := readReplicaCandidate(w, r)
	if err != nil {
		operationError(w, err, "replica full publication")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/replica/publication/")
	req, err := replica.ParseFullPublicationOperation(action, raw)
	if err != nil {
		operationError(w, err, "replica full publication")
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
		operationError(w, err, "replica full publication")
		return
	}
	executable, err := os.Executable()
	if err != nil {
		operationError(w, err, "replica full publication")
		return
	}
	items, err := m.RunFullPublicationOperation(r.Context(), action, req, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil {
		operationError(w, err, "replica full publication")
		return
	}
	out := result.New("replica publication "+action, items)
	out.Summary = "依本次明确操作处理完整批次；授权、检查、提案与共同确认分别进行，发布者只读，历史权限不回放"
	writeJSON(w, 200, out)
}
