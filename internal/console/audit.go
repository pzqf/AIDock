package console

import (
	"net/http"
	"strconv"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/result"
)

func (s *Server) auditAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values := r.URL.Query()
	q := auditlog.Query{Category: values.Get("category"), Search: values.Get("search"), Page: 1}
	for _, key := range []string{"category", "search", "page"} {
		if len(values[key]) > 1 {
			operationError(w, result.Fail("INVALID_INPUT", "审计查询不接受重复条件", 2), "audit")
			return
		}
	}
	if text := values.Get("page"); text != "" {
		var err error
		if q.Page, err = strconv.Atoi(text); err != nil {
			operationError(w, result.Fail("INVALID_INPUT", "审计页码须为整数", 2), "audit")
			return
		}
	}
	v, err := s.requestEngine(r).Audit(r.Context(), q)
	if err != nil {
		operationError(w, err, "audit")
		return
	}
	// Reading succeeded even when a chain is damaged. Preserve the report's
	// status without making the console discard the other verified chains.
	v.OK = true
	writeJSON(w, http.StatusOK, v)
}
