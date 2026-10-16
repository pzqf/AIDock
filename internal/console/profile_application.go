package console

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) profileApplicationAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/native/profile/review" || r.URL.Path == "/api/native/profile/history"
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
	e := s.requestEngine(r)
	m, err := e.Native()
	if err != nil {
		operationError(w, err, "tool profile")
		return
	}
	var raw []byte
	if !read {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 请求", 2), "tool profile")
			return
		}
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if err != nil || !json.Valid(raw) {
			operationError(w, result.Fail("INVALID_INPUT", "需要大小受限的单个 JSON 对象", 2), "tool profile")
			return
		}
	}
	var out any
	switch r.URL.Path {
	case "/api/native/profile/prepare":
		var req native.CheckRequest
		req, err = native.ParseProfilePreparationRequest(raw)
		if err == nil {
			var executable string
			executable, err = os.Executable()
			if err == nil {
				out, err = m.PrepareProfile(r.Context(), req, executable, e.Roots.Home)
			}
		}
	case "/api/native/profile/review", "/api/native/profile/plan":
		var executable string
		executable, err = os.Executable()
		if err == nil {
			if read {
				out, err = m.ReviewProfileApplication(r.Context(), executable, e.Roots.Home)
			} else {
				var req native.ProfileApplicationRequest
				req, err = native.ParseProfileApplicationRequest(raw)
				if err == nil {
					out, err = m.ProfileApplicationPlan(r.Context(), req, executable, e.Roots.Home)
				}
			}
		}
	case "/api/native/profile/history":
		out, err = m.ProfileApplicationHistory()
	default:
		var fields map[string]any
		switch r.URL.Path {
		case "/api/native/profile/apply", "/api/native/profile/rollback/apply":
			fields, err = decodeConnectionControl(raw, "plan_id", "digest")
		case "/api/native/profile/rollback/plan":
			fields, err = decodeConnectionControl(raw, "transaction")
		case "/api/native/profile/recover":
			fields, err = decodeConnectionControl(raw)
		}
		if err == nil {
			text := func(key string) string { v, _ := fields[key].(string); return v }
			switch r.URL.Path {
			case "/api/native/profile/apply":
				out, err = m.ApplyProfileApplication(r.Context(), text("plan_id"), text("digest"))
			case "/api/native/profile/rollback/plan":
				out, err = m.ReverseProfileApplication(r.Context(), text("transaction"))
			case "/api/native/profile/rollback/apply":
				out, err = m.ApplyProfileApplicationReverse(r.Context(), text("plan_id"), text("digest"))
			case "/api/native/profile/recover":
				out, err = m.RecoverProfileApplication()
			}
		}
	}
	if err != nil {
		operationError(w, err, "tool profile")
		return
	}
	v := result.New("tool profile", out)
	v.Summary = "技能、规则和当前有效 MCP 连接的共同预览、事务与恢复；不启动程序，不提升 L3/L4"
	writeJSON(w, http.StatusOK, v)
}
