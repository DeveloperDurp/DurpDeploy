package handler_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestLifecycleSharedEditorDoesNotReplaceSettingsModal(t *testing.T) {
	h := newProjectHarness(t)
	lifecycle := h.makeLifecycle("shared settings")
	path := h.server.URL + "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)
	for _, modal := range []bool{false, true} {
		request, err := http.NewRequestWithContext(
			t.Context(),
			"GET",
			path,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if modal {
			request.Header.Set("HX-Request", "true")
			request.Header.Set("HX-Target", "project-edit-content")
		}
		response, err := h.authedClient().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal("lifecycle read failed", err)
		}
		if !strings.Contains(string(body), `id="lifecycle-settings-form"`) {
			t.Fatal("settings form missing")
		}
		shared := strings.Contains(string(body), `id="shared-variables"`)
		if shared == modal {
			t.Fatalf("shared editor present=%t, modal=%t", shared, modal)
		}
	}
}
