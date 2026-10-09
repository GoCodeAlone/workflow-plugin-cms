package internal

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func (a *AdminAPI) pageHistory(w http.ResponseWriter, r *http.Request, tenantID int64) {
	w.Header().Set("Cache-Control", "private, no-store")
	// Historical drafts contain private content. A new read endpoint must not
	// inherit the legacy direct-handler assumption that an outer host gates it.
	if a.RequestAccess == nil || a.TenantAccess == nil {
		writeJSONError(w, 503, "unavailable", "history authorization not configured")
		return
	}
	history, ok := a.pages.(store.PageHistoryStore)
	if !ok {
		writeJSONError(w, 503, "unavailable", "page history not configured")
		return
	}
	q := store.PageHistoryQuery{Limit: 20}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSONError(w, 400, "bad_request", "canonical history query required")
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "after_revision" && key != "limit") {
			writeJSONError(w, 400, "bad_request", "canonical history query required")
			return
		}
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != values[0] || n < 0 {
			writeJSONError(w, 400, "bad_request", "canonical history query required")
			return
		}
		if key == "after_revision" {
			q.AfterRevision = n
		} else {
			if n < 1 || n > 100 {
				writeJSONError(w, 400, "bad_request", "history limit must be between 1 and 100")
				return
			}
			q.Limit = int(n)
		}
	}
	h, err := history.ReadPageHistory(r.Context(), tenantID, q)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, 404, "not_found", "page history not found")
		return
	}
	if err != nil {
		writeJSONError(w, 503, "unavailable", "page history unavailable")
		return
	}
	writeJSON(w, 200, h)
}
