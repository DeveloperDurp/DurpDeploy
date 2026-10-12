package handler

import (
	"net/http"

	"durpdeploy/internal/notify"
)

func notificationCredential(r *http.Request, name string) string {
	return notify.CredentialValue(
		r.FormValue(name), notificationClearFlag(r, "clear_"+name) == 1,
	)
}

func notificationClearFlag(r *http.Request, name string) int64 {
	if r.FormValue(name) == "on" {
		return 1
	}
	return 0
}
