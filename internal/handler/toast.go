package handler

import (
	"encoding/json"
	"net/http"
)

const (
	ToastInfo    = "info"
	ToastSuccess = "success"
	ToastWarning = "warning"
	ToastDanger  = "danger"
)

type Toast struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// SetToastHeader sets the HX-Trigger header with a makeToast event.
func SetToastHeader(w http.ResponseWriter, level, message string) {
	eventMap := map[string]Toast{
		"makeToast": {Level: level, Message: message},
	}
	data, _ := json.Marshal(eventMap)
	w.Header().Set("HX-Trigger", string(data))
}

// SetToastSuccess sets a success toast header.
func SetToastSuccess(w http.ResponseWriter, message string) {
	SetToastHeader(w, ToastSuccess, message)
}

// SetToastError sets an error toast header.
func SetToastError(w http.ResponseWriter, message string) {
	SetToastHeader(w, ToastDanger, message)
}
