package httpx

import (
	"net/http"
	"strconv"
)

// HistoryPage retains the first hundred rows for existing clients. The
// table's largest selectable page size is 200.
func HistoryPage(r *http.Request) (limit, offset int) {
	query := r.URL.Query()
	limit, _ = strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ = strconv.Atoi(query.Get("offset"))
	return min(limit, 200), max(offset, 0)
}
