package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestContainerImageAPI_RejectsRunnerIncompatibleImages(t *testing.T) {
	images := []string{
		"-alpine", "alpine :3", " alpine:3", "alpine:3\n", "alpine:3\x00",
	}
	for _, image := range images {
		t.Run(fmt.Sprintf("%q", image), func(t *testing.T) {
			imageJSON, err := json.Marshal(image)
			if err != nil {
				t.Fatal(err)
			}
			h := newHarness(t)
			token := h.adminToken(t)
			project := h.seedProject(
				t,
				h.seedUser(t, "owner@example.com", "admin"),
			)
			for _, tc := range []struct {
				path string
				body string
			}{
				{
					path: fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
					body: fmt.Sprintf(
						`{"name":"step","script_body":"true","container_image":%s}`,
						imageJSON,
					),
				},
				{
					path: "/api/v1/templates",
					body: fmt.Sprintf(
						`{"name":"template","script_body":"true","container_image":%s}`,
						imageJSON,
					),
				},
			} {
				t.Run(tc.path, func(t *testing.T) {
					rec := h.request(
						t,
						http.MethodPost,
						tc.path,
						token,
						tc.body,
					)
					h.assertStatus(t, rec, http.StatusBadRequest)
					if !strings.Contains(
						strings.ToLower(
							rec.Body.String(),
						),
						"invalid container image",
					) {
						t.Fatalf(
							"unexpected validation error: %s",
							rec.Body.String(),
						)
					}
				})
			}
			request, runbookProjectID := newRunbookContainerRouter(t)
			base := fmt.Sprintf("/api/v1/projects/%d", runbookProjectID)
			rec := request(
				http.MethodPost, base+"/runbooks",
				fmt.Sprintf(
					`{"name":"book","steps":[{"name":"run",`+
						`"script_body":"true","container_image":%s}]}`,
					imageJSON,
				),
			)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"runbook status=%d body=%s",
					rec.Code,
					rec.Body.String(),
				)
			}
			if !strings.Contains(rec.Body.String(), "invalid container image") {
				t.Fatalf(
					"unexpected runbook validation error: %s",
					rec.Body.String(),
				)
			}
			releases := request(http.MethodGet, base+"/releases", "")
			if releases.Code != http.StatusOK {
				t.Fatalf("release list status=%d body=%s",
					releases.Code, releases.Body.String())
			}
			var list struct {
				Items []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(
				releases.Body.Bytes(),
				&list,
			); err != nil ||
				len(list.Items) != 0 {
				t.Fatalf("releases=%d error=%v", len(list.Items), err)
			}
		})
	}
}
