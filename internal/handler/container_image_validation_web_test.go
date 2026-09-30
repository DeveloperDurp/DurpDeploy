package handler_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestContainerImageWeb_RejectsRunnerIncompatibleImages(t *testing.T) {
	images := []string{
		"-alpine", "alpine :3", " alpine:3", "alpine:3\n", "alpine:3\x00",
	}
	for _, image := range images {
		t.Run(fmt.Sprintf("%q", image), func(t *testing.T) {
			h := newProjectHarness(t)
			project := h.makeProject("invalid-image")
			step, err := h.authedClient().PostForm(
				fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
				url.Values{
					"csrf_token": {h.csrfToken()}, "name": {"step"},
					"script_body": {"true"}, "execution_target": {"local"},
					"container_image": {image},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			stepBody := readBody(t, step)
			step.Body.Close()
			if step.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("step status=%d", step.StatusCode)
			}
			if !strings.Contains(stepBody, "Invalid container image") {
				t.Fatalf("unexpected step validation error: %s", stepBody)
			}
			book, err := h.authedClient().PostForm(
				fmt.Sprintf("%s/projects/%d/runbooks", h.server.URL, project.ID),
				url.Values{
					"csrf_token": {h.csrfToken()}, "name": {"book"},
					"step_name": {"step"}, "step_script": {"true"},
					"step_interpreter": {"bash"}, "step_timeout": {"0"},
					"step_retries": {"0"}, "step_target": {"local"},
					"step_selectors": {""}, "step_image": {image},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			bookBody := readBody(t, book)
			book.Body.Close()
			if book.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("runbook status=%d", book.StatusCode)
			}
			if !strings.Contains(bookBody, "invalid container image") {
				t.Fatalf("unexpected runbook validation error: %s", bookBody)
			}
			steps, err := h.repo.Queries.ListStepsByProject(
				t.Context(),
				project.ID,
			)
			if err != nil || len(steps) != 0 {
				t.Fatalf("steps=%v error=%v", steps, err)
			}
			releases, err := h.repo.Queries.CountReleasesByProject(
				t.Context(), project.ID,
			)
			if err != nil || releases != 0 {
				t.Fatalf("releases=%d error=%v", releases, err)
			}
		})
	}
}
