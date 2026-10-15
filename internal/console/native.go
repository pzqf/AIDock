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
	m, err := s.requestEngine(r).Native()
	if err != nil {
		operationError(w, err, "native preview")
		return
	}
	var p native.Plan
	var items any
	if r.URL.Path == "/api/native/prepare" {
		var req native.CheckRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Prepare(r.Context(), req)
		}
	} else if r.URL.Path == "/api/native/plan" {
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
	if r.URL.Path != "/api/native/prepare" {
		items = p
	}
	if r.URL.Path == "/api/native/apply" {
		items, err = m.History()
		if err != nil {
			operationError(w, err, "native apply")
			return
		}
	}
	// A completed diagnostic can contain blocked targets. Return its complete
	// report so the console can render them just like the CLI's plan result.
	v := result.New("native preview", items)
	v.Summary = p.Boundary
	if r.URL.Path == "/api/native/prepare" {
		v.Operation, v.Summary = "tool prepare", items.(native.PrepareReport).Boundary
	}
	if r.URL.Path == "/api/native/apply" {
		v.Summary = "计划中的技能与规则文件已安装到绑定工具项目；实际会话生效未验证，可从工具事务回退"
	}
	writeJSON(w, 200, v)
}

func (s *Server) nativeCheckAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/native/check/latest"
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
		operationError(w, err, "tool check")
		return
	}
	var items any
	if read {
		items, err = m.Latest(r.Context())
	} else {
		var req native.CheckRequest
		err = decodeSource(w, r, &req)
		if err == nil {
			items, err = m.Check(r.Context(), req)
		}
	}
	if err != nil {
		operationError(w, err, "tool check")
		return
	}
	// The request succeeded; the report retains UNKNOWN and FAIL separately.
	v := result.New("tool check", items)
	v.Summary = "项目文件状态与隔离工具发现检查；实际项目会话生效尚未验证"
	writeJSON(w, 200, v)
}

func (s *Server) nativeConfigurationAPI(w http.ResponseWriter, r *http.Request) {
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
		operationError(w, err, "tool config")
		return
	}
	var req struct {
		Tools []string `json:"tools"`
	}
	if err = decodeSource(w, r, &req); err != nil {
		operationError(w, err, "tool config")
		return
	}
	report, err := m.Configuration(r.Context(), req.Tools)
	if err != nil {
		operationError(w, err, "tool config")
		return
	}
	// A successful read can report malformed configuration; preserve all files'
	// results so users can fix one tool without losing the other tool's report.
	v := result.New("tool config", report)
	v.Summary = report.Boundary
	writeJSON(w, 200, v)
}
