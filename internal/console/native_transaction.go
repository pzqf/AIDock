package console

import (
	"github.com/pzqf/AIDock/internal/result"
	"net/http"
)

func (s *Server) nativeRehearsalAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/native/rehearsal/latest"
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
	m, err := s.requestEngine(r).Native()
	if err != nil {
		operationError(w, err, "tool rehearsal")
		return
	}
	var items any
	if read {
		items, err = m.RehearsalLatest()
	} else {
		switch r.URL.Path {
		case "/api/native/rehearsal/run":
			var req struct {
				ID     string `json:"plan_id"`
				Digest string `json:"digest"`
			}
			err = decodeSource(w, r, &req)
			if err == nil {
				items, err = m.Rehearse(r.Context(), req.ID, req.Digest)
			}
		case "/api/native/rehearsal/plan":
			var req struct {
				Session     string `json:"session_id"`
				Transaction string `json:"transaction_id"`
			}
			err = decodeSource(w, r, &req)
			if err == nil {
				items, err = m.RehearsalReverse(req.Session, req.Transaction)
			}
		case "/api/native/rehearsal/apply":
			var req struct {
				Session string `json:"session_id"`
				ID      string `json:"plan_id"`
				Digest  string `json:"digest"`
			}
			err = decodeSource(w, r, &req)
			if err == nil {
				items, err = m.RehearsalApply(r.Context(), req.Session, req.ID, req.Digest)
			}
		case "/api/native/rehearsal/recover":
			var req struct {
				Session string `json:"session_id"`
			}
			err = decodeSource(w, r, &req)
			if err == nil {
				items, err = m.RehearsalRecover(req.Session)
			}
		}
	}
	if err != nil {
		operationError(w, err, "tool rehearsal")
		return
	}
	v := result.New("tool rehearsal", items)
	v.Summary = "仅操作独立预演项目；实际工具目标与技能源保留"
	writeJSON(w, 200, v)
}

func (s *Server) nativeTransactionsAPI(w http.ResponseWriter, r *http.Request) {
	recover := r.URL.Path == "/api/native/recover"
	if recover && r.Method != http.MethodPost || !recover && r.Method != http.MethodGet {
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
	m, err := s.requestEngine(r).Native()
	if err != nil {
		operationError(w, err, "tool transactions")
		return
	}
	var items any
	if recover {
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.RecoverApplication()
		}
	} else {
		items, err = m.ApplicationHistory()
	}
	if err != nil {
		operationError(w, err, "tool transactions")
		return
	}
	v := result.New("tool transactions", items)
	v.Summary = "当前绑定的工具事务；预演记录独立保存"
	writeJSON(w, 200, v)
}

func (s *Server) nativeReverseAPI(w http.ResponseWriter, r *http.Request) {
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
	m, err := s.requestEngine(r).Native()
	if err != nil {
		operationError(w, err, "tool rollback")
		return
	}
	var items any
	if r.URL.Path == "/api/native/rollback/plan" {
		var req struct {
			Transaction string `json:"transaction_id"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ReverseApplication(r.Context(), req.Transaction)
		}
	} else {
		var req struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.ApplyApplicationReverse(r.Context(), req.ID, req.Digest)
		}
	}
	if err != nil {
		operationError(w, err, "tool rollback")
		return
	}
	v := result.New("tool rollback", items)
	v.Summary = "工具文件回退/撤销；技能源与实际工具会话不变"
	writeJSON(w, 200, v)
}
