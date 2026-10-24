package repository

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runLibraryScript(
	t *testing.T,
	script string,
	env []string,
	success bool,
) string {
	return runLibraryInterpreter(t, "bash", script, env, success)
}

func runLibraryInterpreter(
	t *testing.T,
	interpreter, script string,
	env []string,
	success bool,
) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	arguments := []string{"-c", script}
	if interpreter == "pwsh" {
		arguments = []string{
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			"-",
		}
	}
	cmd := exec.CommandContext(ctx, interpreter, arguments...)
	if interpreter == "pwsh" {
		cmd.Stdin = strings.NewReader(script)
	}
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if (err == nil) != success {
		t.Fatalf(
			"starter script success=%t, expected=%t: %v: %s",
			err == nil,
			success,
			err,
			out,
		)
	}
	return string(out)
}

func TestTemplateLibraryLanguageScripts(t *testing.T) {
	for _, language := range []struct {
		suffix      string
		interpreter string
	}{
		{suffix: "-powershell", interpreter: "pwsh"},
		{suffix: "-python", interpreter: "python3"},
	} {
		t.Run(language.interpreter, func(t *testing.T) {
			if _, err := exec.LookPath(language.interpreter); err != nil {
				t.Skip(
					"host interpreter unavailable; container E2E runs this interpreter",
				)
			}
			checkLibraryLanguageWorkflow(
				t,
				language.suffix,
				language.interpreter,
			)
		})
	}
}

func checkLibraryLanguageWorkflow(t *testing.T, suffix, interpreter string) {
	t.Helper()
	root := t.TempDir()
	server := httptest.NewTLSServer(http.HandlerFunc(serveLibraryArtifact))
	defer server.Close()
	certificate := filepath.Join(root, "ca.pem")
	writeLibraryFixture(t, certificate, string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})), 0600)
	log := filepath.Join(root, "commands")
	command := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$CALL_LOG\"\nexit \"${FIXTURE_EXIT:-0}\"\n"
	writeLibraryFixture(t, filepath.Join(root, "systemctl"), command, 0700)
	writeLibraryFixture(t, filepath.Join(root, "docker"), command, 0700)
	manifest := filepath.Join(root, "compose file.yml")
	writeLibraryFixture(t, manifest, "services: {}", 0600)
	destination := filepath.Join(root, "installed app")
	env := []string{
		"PATH=" + root + ":" + os.Getenv("PATH"), "CALL_LOG=" + log,
		"DURPDEPLOY_STAGE_DIR=" + root, "SSL_CERT_FILE=" + certificate,
		"ARTIFACT_URL=" + server.URL + "/download", "ARTIFACT_TOKEN=fixture-secret",
		"FILE_SOURCE=", "INSTALL_PATH=" + destination, "SERVICE_NAME=example.service",
		"HEALTH_URL=" + server.URL + "/health", "COMPOSE_FILE=" + manifest,
		"COMPOSE_PROJECT=example", "APP_IMAGE=example/app:1.0",
	}
	for _, slug := range []string{"linux-service", "compose-application"} {
		entry, err := GetLibraryTemplate(slug + suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range entry.Snapshot.Steps {
			out := runLibraryInterpreter(
				t,
				interpreter,
				step.ScriptBody,
				env,
				true,
			)
			if strings.Contains(out, "fixture-secret") {
				t.Fatal("script leaked a token")
			}
		}
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "downloaded executable fixture" {
		t.Fatal("language workflow lost artifact bytes", err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("wrong file mode", err)
	}
	commands, err := os.ReadFile(log)
	expected := "--no-ask-password\nrestart\n--\nexample.service\n" +
		"--no-ask-password\nis-active\n--quiet\n--\nexample.service\n" +
		"compose\n--file\n" + manifest + "\n--project-name\nexample\npull\n" +
		"compose\n--file\n" + manifest + "\n--project-name\nexample\nup\n--detach\n"
	if err != nil || string(commands) != expected {
		t.Fatal("lost argument boundaries", err)
	}
	checkLibraryLanguageFailures(t, suffix, interpreter, env, server.URL, root)
}

func checkLibraryLanguageFailures(
	t *testing.T,
	suffix, interpreter string,
	env []string,
	serverURL, root string,
) {
	t.Helper()
	for _, scenario := range []struct {
		slug     string
		override string
	}{
		{slug: "artifact-download", override: "ARTIFACT_URL=http://127.0.0.1/artifact"},
		{slug: "artifact-download", override: "ARTIFACT_TOKEN=bad\nheader"},
		{slug: "artifact-download", override: "ARTIFACT_URL=" + serverURL + "/failure"},
		{slug: "file-copy", override: "INSTALL_PATH=relative"},
		{slug: "service-restart", override: "SERVICE_NAME=example;false"},
		{slug: "http-health-check", override: "HEALTH_URL=file:///etc/passwd"},
		{slug: "service-restart", override: "FIXTURE_EXIT=9"},
		{slug: "compose-application", override: "FIXTURE_EXIT=9"},
		{slug: "compose-application", override: "COMPOSE_PROJECT=--help"},
	} {
		entry, err := GetLibraryTemplate(scenario.slug + suffix)
		if err != nil {
			t.Fatal(err)
		}
		runLibraryInterpreter(
			t,
			interpreter,
			entry.Snapshot.Steps[0].ScriptBody,
			append(env, scenario.override),
			false,
		)
	}
	contents, err := os.ReadFile(filepath.Join(root, "artifact"))
	if err != nil || string(contents) != "downloaded executable fixture" {
		t.Fatal("failed download replaced the previous artifact", err)
	}
}

func TestTemplateLibraryLinuxWorkflowScripts(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewTLSServer(http.HandlerFunc(serveLibraryArtifact))
	defer server.Close()
	certificate := filepath.Join(root, "ca.pem")
	writeLibraryFixture(t, certificate, string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})), 0600)
	log := filepath.Join(root, "calls")
	writeLibraryFixture(t, filepath.Join(root, "systemctl"),
		"#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$CALL_LOG\"\n", 0700)
	destination := filepath.Join(root, "app")
	env := []string{"PATH=" + root + ":" + os.Getenv("PATH"), "CALL_LOG=" + log,
		"DURPDEPLOY_STAGE_DIR=" + root, "ARTIFACT_URL=" + server.URL + "/download",
		"ARTIFACT_TOKEN=fixture-secret", "CURL_CA_BUNDLE=" + certificate,
		"INSTALL_PATH=" + destination, "SERVICE_NAME=example.service", "HEALTH_URL=" + server.URL + "/health"}
	entry, err := GetLibraryTemplate("linux-service")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range entry.Snapshot.Steps {
		out := runLibraryScript(t, step.ScriptBody, env, true)
		if strings.Contains(out, "fixture-secret") {
			t.Fatal("starter script logged an authentication secret")
		}
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "downloaded executable fixture" {
		t.Fatal("Linux workflow did not install the downloaded artifact", err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("installed artifact has wrong permissions", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil ||
		string(
			commands,
		) != "--no-ask-password\nrestart\n--\nexample.service\n--no-ask-password\nis-active\n--quiet\n--\nexample.service\n" {
		t.Fatal("service restart did not preserve argument boundaries", err)
	}
}

func serveLibraryArtifact(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/failure" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/download" {
		http.Redirect(w, r, "/artifact", http.StatusFound)
		return
	}
	if r.URL.Path != "/artifact" {
		return
	}
	if r.Header.Get("Authorization") != "Bearer fixture-secret" {
		w.WriteHeader(401)
		return
	}
	_, _ = w.Write([]byte("downloaded executable fixture"))
}

func writeLibraryFixture(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateLibraryComposeWorkflowScripts(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "commands")
	manifest := filepath.Join(root, "compose file.yml")
	writeLibraryFixture(
		t,
		manifest,
		"services:\n  app:\n    image: ${APP_IMAGE}\n",
		0600,
	)
	writeLibraryFixture(
		t,
		filepath.Join(root, "docker"),
		"#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$CALL_LOG\"\nexit \"${FIXTURE_EXIT:-0}\"\n",
		0700,
	)
	env := []string{"PATH=" + root + ":" + os.Getenv("PATH"), "CALL_LOG=" + log,
		"COMPOSE_FILE=" + manifest, "COMPOSE_PROJECT=example", "APP_IMAGE=example/app:1.0"}
	for _, script := range []string{libraryComposePull, libraryComposeUp} {
		runLibraryScript(t, script, env, true)
		runLibraryScript(t, script, append(env, "FIXTURE_EXIT=9"), false)
	}
	commands, err := os.ReadFile(log)
	if err != nil ||
		!strings.Contains(
			string(commands),
			"compose\n--file\n"+manifest+"\n--project-name\nexample\npull\n",
		) ||
		!strings.Contains(string(commands), "example\nup\n--detach\n") {
		t.Fatal("Compose workflow lost configured argument boundaries", err)
	}
}

func TestTemplateLibraryScriptsRejectUnsafeInput(t *testing.T) {
	root := t.TempDir()
	for _, scenario := range []struct {
		script string
		env    []string
	}{
		{libraryDownload, []string{"DURPDEPLOY_STAGE_DIR=" + root, "ARTIFACT_URL=file:///etc/passwd"}},
		{libraryDownload, []string{"DURPDEPLOY_STAGE_DIR=" + root, "ARTIFACT_URL=https://example.test", "ARTIFACT_TOKEN=bad\nheader"}},
		{libraryCopy, []string{"DURPDEPLOY_STAGE_DIR=" + root, "INSTALL_PATH=relative"}},
		{libraryRestart, []string{"SERVICE_NAME=--help"}},
		{libraryComposeUp, []string{"COMPOSE_FILE=/missing", "COMPOSE_PROJECT=example", "APP_IMAGE=example/app"}},
	} {
		runLibraryScript(t, scenario.script, scenario.env, false)
	}
}

func TestTemplateLibraryDownloadFailurePreservesArtifact(t *testing.T) {
	for _, status := range []int{302, 304, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			checkLibraryDownloadFailurePreservesArtifact(t, status)
		})
	}
}

func checkLibraryDownloadFailurePreservesArtifact(t *testing.T, status int) {
	t.Helper()
	root := t.TempDir()
	writeLibraryFixture(
		t,
		filepath.Join(root, "artifact"),
		"keep existing artifact",
		0600,
	)
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}),
	)
	defer server.Close()
	cert := filepath.Join(root, "ca.pem")
	writeLibraryFixture(t, cert, string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})), 0600)
	runLibraryScript(
		t,
		libraryDownload,
		[]string{"DURPDEPLOY_STAGE_DIR=" + root,
			"ARTIFACT_URL=" + server.URL, "CURL_CA_BUNDLE=" + cert},
		false,
	)
	data, err := os.ReadFile(filepath.Join(root, "artifact"))
	if err != nil || string(data) != "keep existing artifact" {
		t.Fatal("failed download replaced an existing artifact", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "artifact.*"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed download left temporary files", err)
	}
}

func TestTemplateLibraryDownloadRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "artifact")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "keep")
	writeLibraryFixture(t, marker, "existing directory", 0600)
	server := httptest.NewTLSServer(http.HandlerFunc(serveLibraryArtifact))
	defer server.Close()
	certificate := filepath.Join(root, "ca.pem")
	writeLibraryFixture(t, certificate, string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})), 0600)
	runLibraryScript(t, libraryDownload, []string{
		"DURPDEPLOY_STAGE_DIR=" + root,
		"ARTIFACT_URL=" + server.URL + "/download",
		"ARTIFACT_TOKEN=fixture-secret", "CURL_CA_BUNDLE=" + certificate,
	}, false)
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "existing directory" {
		t.Fatal("download changed the existing directory", err)
	}
	files, err := os.ReadDir(destination)
	if err != nil || len(files) != 1 {
		t.Fatal("download nested a temporary artifact in the directory", err)
	}
	temporary, err := filepath.Glob(filepath.Join(root, "artifact.*"))
	if err != nil || len(temporary) != 0 {
		t.Fatal("rejected download left a temporary file", err)
	}
}

func TestTemplateLibraryFileCopyRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	writeLibraryFixture(t, source, "keep inside source", 0600)
	runLibraryScript(
		t,
		libraryCopy,
		[]string{"FILE_SOURCE=" + source, "INSTALL_PATH=" + destination},
		false,
	)
	if _, err := os.Stat(filepath.Join(destination, "source")); !os.IsNotExist(
		err,
	) {
		t.Fatal("file installation accepted a directory destination")
	}
}

func TestTemplateLibraryHealthRequiresSuccessResponse(t *testing.T) {
	for _, status := range []int{200, 204, 301, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
				}),
			)
			defer server.Close()
			runLibraryScript(
				t,
				libraryHealth,
				[]string{"HEALTH_URL=" + server.URL},
				status/100 == 2,
			)
		})
	}
}
