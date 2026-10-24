//go:build e2e

package api_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"github.com/google/uuid"
)

func TestTemplateLibraryDownloadDirectoryE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.completionTimeout = 2 * time.Minute
	var entry repository.LibraryTemplate
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/template-library/artifact-download", nil, 200),
		&entry,
	)
	// HTTP success is isolated here; repository tests exercise real TLS.
	prefix := `ARTIFACT_URL=https://fixture.invalid/artifact
mkdir "$DURPDEPLOY_STAGE_DIR/artifact"
curl() {
    printf '%s' payload > "$temporary_file"
    printf '%s' 200
}
`
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Directory collision", "interpreter": "bash",
		"script_body":     prefix + entry.Snapshot.Steps[0].ScriptBody,
		"container_image": "harbor.durp.info/dockerhub/library/python:3.12",
	}, 201)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "directory-collision"),
	)
	libraryRuntimeDiagnostics(t, f, deployment.ID)
	f.completion(t, deployment.ID, events.DeploymentFailed)
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "cannot overwrite directory") ||
		strings.Contains(
			string(logs),
			"Artifact downloaded to managed staging",
		) {
		t.Fatal("download accepted a directory destination", string(logs))
	}
}

// Container checks remain mandatory when a CI worker has no host PowerShell.
func TestTemplateLibraryLanguageContainerE2E(t *testing.T) {
	for _, language := range []struct {
		suffix string
		image  string
	}{
		{suffix: "powershell", image: "harbor.durp.info/microsoft/powershell:latest"},
		{suffix: "python", image: "harbor.durp.info/dockerhub/library/python:3.12"},
	} {
		t.Run(language.suffix, func(t *testing.T) {
			checkLibraryLanguageContainers(t, language.suffix, language.image)
		})
	}
}

func checkLibraryLanguageContainers(t *testing.T, suffix, image string) {
	t.Helper()
	f := newArtifactE2E(t)
	f.completionTimeout = 3 * time.Minute
	upstream, client, _, _ := artifactHTTPSFixture(t)
	response, err := client.HTTP.Get(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: response.TLS.PeerCertificates[0].Raw,
	})
	image = libraryLanguageFixtureImage(t, f, image, certificate)
	health := verificationUpstream(
		t,
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
		),
	)
	values := map[string]string{
		"ARTIFACT_URL": libraryContainerURL(
			t,
			upstream,
		), "ARTIFACT_TOKEN": "artifact-secret",
		"INSTALL_PATH": "/tmp/installed-app", "SERVICE_NAME": "example.service",
		"HEALTH_URL": libraryContainerURL(
			t,
			health,
		), "COMPOSE_FILE": "/tmp/compose.yml",
		"COMPOSE_PROJECT": "example", "APP_IMAGE": "example/app:1.0",
	}
	for name, value := range values {
		f.api(t, "POST", f.base()+"/variables", map[string]any{
			"name": name, "value": value, "secret": name == "ARTIFACT_TOKEN",
		}, 201)
	}
	prefix := libraryRuntimePrefix(suffix)
	order := 0
	for _, slug := range []string{"linux-service", "compose-application"} {
		entry, err := repository.GetLibraryTemplate(slug + "-" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range entry.Snapshot.Steps {
			order++
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": step.Name, "script_body": prefix + step.ScriptBody,
				"interpreter": step.Interpreter, "container_image": image,
				"network_mode": "bridge", "sort_order": order,
			}, 201)
		}
	}
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "language-runtime"),
	)
	libraryRuntimeDiagnostics(t, f, deployment.ID)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	for _, marker := range []string{"Artifact downloaded", "File installed", "Service is active", "HTTP health check passed", "compose --file /tmp/compose.yml --project-name example pull", "compose --file /tmp/compose.yml --project-name example up --detach"} {
		if !strings.Contains(logs, marker) {
			t.Fatalf("missing runtime marker %q: %s", marker, logs)
		}
	}
	if strings.Contains(logs, "artifact-secret") {
		t.Fatal("runtime leaked artifact token")
	}
}

func libraryLanguageFixtureImage(
	t *testing.T,
	f *artifactE2E,
	base string,
	certificate []byte,
) string {
	t.Helper()
	context := t.TempDir()
	if err := os.WriteFile(filepath.Join(context, "ca.pem"), certificate, 0644); err != nil {
		t.Fatal(err)
	}
	// Bake privileged-command stubs into the executable image filesystem.
	// Docker mounts the workload's writable /tmp with noexec by default.
	command := "#!/bin/sh\nprintf '%s\\n' \"$*\"\n"
	if err := os.WriteFile(filepath.Join(context, "command"), []byte(command), 0644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM " + base + `
COPY ca.pem /opt/library-ca.pem
COPY --chmod=0755 command /opt/library-bin/docker
COPY --chmod=0755 command /opt/library-bin/systemctl
ENV SSL_CERT_FILE=/opt/library-ca.pem
ENV PATH=/opt/library-bin:${PATH}
`
	if err := os.WriteFile(filepath.Join(context, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatal(err)
	}
	image := "localhost/durpdeploy-library-language:" + uuid.NewString()
	stagingRuntime(t, "build", "--tag", image, context)
	t.Cleanup(
		func() { f.h.runner.KillAll(); stagingRuntime(t, "image", "rm", image) },
	)
	return image
}

func libraryContainerURL(t *testing.T, address string) string {
	t.Helper()
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") != "podman" {
		return address
	}
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "host.containers.internal:" + parsed.Port()
	return parsed.String()
}

func libraryRuntimeDiagnostics(
	t *testing.T,
	f *artifactE2E,
	deploymentID int64,
) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			ctx, cancel := context.WithTimeout(
				context.WithoutCancel(t.Context()),
				3*time.Second,
			)
			defer cancel()
			logs, err := f.h.repo.Queries.ListDeploymentLogsByDeployment(
				ctx,
				deploymentID,
			)
			if err != nil {
				t.Log(err)
				return
			}
			for _, line := range logs {
				t.Log(line.Line)
			}
		}
	})
}

func libraryRuntimePrefix(suffix string) string {
	if suffix == "powershell" {
		return "[System.IO.File]::WriteAllText('/tmp/compose.yml','services: {}')\n"
	}
	return "with open('/tmp/compose.yml','w') as fixture: fixture.write('services: {}')\n"
}
