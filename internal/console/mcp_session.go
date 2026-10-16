package console

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func (s *Server) mcpSessionAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/mcp/session/state"
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
		operationError(w, err, "mcp session")
		return
	}
	var out any
	switch r.URL.Path {
	case "/api/mcp/session/state":
		out, err = m.MCPSessions(r.Context())
	case "/api/mcp/session/recover":
		var req struct{}
		err = decodeSource(w, r, &req)
		if err == nil {
			var n int
			n, err = m.RecoverMCPSessions()
			out = map[string]int{"recovered": n}
		}
	case "/api/mcp/session/check":
		var req source.MCPSessionRequest
		if r.Header.Get("Content-Type") != "application/json" {
			err = result.Fail("INVALID_INPUT", "需要 JSON 请求", 2)
		} else {
			var raw []byte
			raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
			if err == nil && !json.Valid(raw) {
				err = result.Fail("INVALID_INPUT", "需要单个有效 JSON 对象", 2)
			}
			if err == nil {
				req, err = source.ParseMCPSessionRequest(raw)
			}
		}
		if err == nil {
			out, err = m.CheckMCPSession(r.Context(), req)
		}
	}
	if err != nil {
		operationError(w, err, "mcp session")
		return
	}
	v := result.New("mcp session", out)
	if e, ok := out.(mcpsession.Event); ok {
		v.Summary = mcpsession.Outcome(e.Code)
		if e.Code != "MCP_SESSION_MATCH" {
			v.OK = false
			v.Code = e.Code
		}
	}
	writeJSON(w, 200, v)
}
