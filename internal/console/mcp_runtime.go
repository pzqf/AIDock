package console

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/source"
)

func (s *Server) mcpRuntimeAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/runtime/state"
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
	m, err := s.requestEngine(r).Sources()
	if err != nil {
		operationError(w, err, "mcp runtime")
		return
	}
	var out any
	switch r.URL.Path {
	case "/api/mcp/runtime/state":
		out, err = m.MCPRuntime(r.Context())
	case "/api/mcp/runtime/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPRuntime()
			out = map[string]int{"recovered": n}
		}
	case "/api/mcp/runtime/check":
		var req source.MCPRuntimeRequest
		if r.Header.Get("Content-Type") != "application/json" {
			err = result.Fail("INVALID_INPUT", "需要 JSON 请求", 2)
		} else {
			var raw []byte
			raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
			if err == nil && !json.Valid(raw) {
				err = result.Fail("INVALID_INPUT", "需要单个有效 JSON 对象", 2)
			}
			if err == nil {
				req, err = source.ParseMCPRuntimeRequest(raw)
			}
		}
		if err == nil {
			out, err = m.CheckMCPRuntime(r.Context(), req)
		}
	}
	if err != nil {
		operationError(w, err, "mcp runtime")
		return
	}
	v := result.New("mcp runtime", out)
	if e, ok := out.(runtimecheck.Event); ok && e.Code != "RUNTIME_VERSION_MATCH" {
		v.OK = false
		v.Code = e.Code
	}
	writeJSON(w, 200, v)
}
