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
		"-alpine", "alpine :3", " harbor.durp.info/dockerhub/library/alpine:3", "harbor.durp.info/dockerhub/library/alpine:3\n", "harbor.durp.info/dockerhub/library/alpine:3\x00",
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
			assertContainerImageWebRejection(
				t,
				step,
				"step",
				"Invalid container image",
			)
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
			assertContainerImageWebRejection(
				t,
				book,
				"runbook",
				"invalid container image",
			)
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

func assertContainerImageWebRejection(
	t *testing.T,
	response *http.Response,
	surface, message string,
) {
	t.Helper()
	body := readBody(t, response)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("%s status=%d", surface, response.StatusCode)
	}
	if !strings.Contains(body, message) {
		t.Fatalf("unexpected %s validation error: %s", surface, body)
	}
}
