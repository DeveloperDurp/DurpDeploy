package handler

import (
	"errors"
	"net/http"
	"strconv"
)

// LogPageCursor parses the exclusive upper row ID used by web and API pages.
func LogPageCursor(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("before")
	if raw == "" {
		return 0, nil
	}
	before, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || before <= 0 {
		return 0, errors.New("before must be a positive log ID")
	}
	return before, nil
}
