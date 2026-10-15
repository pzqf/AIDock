package console

import (
	"io"
	"net/http"

	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) credentialAPI(w http.ResponseWriter, r *http.Request) {
	read := r.URL.Path == "/api/credentials/state"
	if read && r.Method != http.MethodGet || !read && r.Method != http.MethodPost {
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
	e := s.requestEngine(r)
	var items any
	var err error
	if read {
		items, err = e.CredentialState(r.Context())
	} else {
		if r.Header.Get("Content-Type") != "application/json" {
			operationError(w, result.Fail("INVALID_INPUT", "需要 JSON 请求", 2), "secret")
			return
		}
		b, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
		if readErr != nil {
			operationError(w, result.Fail("INVALID_INPUT", "密钥请求超过限制或不可读", 2), "secret")
			return
		}
		if r.URL.Path == "/api/credentials/reconcile" {
			// An exact fixed body prevents aliases, duplicate keys or hidden fields.
			if string(b) != "{}" {
				operationError(w, result.Fail("INVALID_INPUT", "核对操作只接受空对象", 2), "secret")
				return
			}
			m, e2 := e.Credentials()
			err = e2
			if err == nil {
				err = m.Reconcile(r.Context())
				items = map[string]bool{"reconciled": err == nil}
			}
		} else {
			var req credentials.Edit
			req, err = credentials.ParseEdit(b)
			if err == nil {
				items, err = e.ChangeCredential(r.Context(), req)
			}
		}
	}
	if err != nil {
		operationError(w, err, "secret")
		return
	}
	v := result.New("secret", items)
	v.Summary = "系统凭据管理；值不随普通快照、回退或同步保存"
	writeJSON(w, 200, v)
}
