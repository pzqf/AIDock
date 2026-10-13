package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func sourceError(w http.ResponseWriter, e error) {
	v := result.New("skill source", nil)
	v.OK = false
	v.Code = "SOURCE_OPERATION_FAILED"
	v.Summary = e.Error()
	var typed *result.Error
	if errors.As(e, &typed) {
		v.Code = typed.Code
		v.Recovery = typed.Recovery
	}
	writeJSON(w, 200, v)
}
func decodeSource(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return result.Fail("INVALID_INPUT", "需要 JSON 请求", 2)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return result.Fail("INVALID_INPUT", e.Error(), 2)
	}
	var tail any
	if e := d.Decode(&tail); e != io.EOF {
		return result.Fail("INVALID_INPUT", "请求必须为单个对象", 2)
	}
	return nil
}
func (s *Server) sourceAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/source/state" || r.URL.Path == "/api/source/file" || r.URL.Path == "/api/source/references"
	if (read && r.Method != http.MethodGet) || (!read && r.Method != http.MethodPost) {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if !read {
		s.workMu.Lock()
		defer s.workMu.Unlock()
		select {
		case <-s.closed:
			http.Error(w, "Console closed", 503)
			return
		default:
		}
	}
	m, e := s.engine.Sources()
	if e != nil {
		sourceError(w, e)
		return
	}
	var items any
	switch r.URL.Path {
	case "/api/source/state":
		items, e = m.State()
	case "/api/source/file":
		items, e = m.File(r.URL.Query().Get("id"), r.URL.Query().Get("path"))
	case "/api/source/references":
		items, e = m.References(r.URL.Query().Get("id"))
	case "/api/source/plan":
		var req source.Request
		e = decodeSource(w, r, &req)
		if e == nil {
			items, e = m.Plan(r.Context(), req)
		}
	case "/api/source/apply":
		var body struct {
			ID     string `json:"plan_id"`
			Digest string `json:"digest"`
		}
		e = decodeSource(w, r, &body)
		if e == nil && body.Digest == "" {
			e = result.Fail("INVALID_INPUT", "需要确认的计划摘要", 2)
		}
		if e == nil {
			j, err := m.Apply(r.Context(), body.ID, body.Digest)
			e = err
			items = map[string]any{"transaction": j.Plan.ID, "state": j.State, "pending_native_apply": true}
		}
	case "/api/source/recover":
		var body struct{}
		e = decodeSource(w, r, &body)
		if e == nil {
			n, err := m.Recover()
			e = err
			items = map[string]int{"recovered": n}
		}
	}
	if e != nil {
		sourceError(w, e)
		return
	}
	v := result.New("skill source", items)
	v.Summary = "仅管理当前显式绑定的技能源；尚未应用到工具或副本"
	writeJSON(w, 200, v)
}
