//go:build e2e && artifactstress && linux

package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type artifactProcessFixture struct {
	api                                                        *artifactE2E
	binary, root, database, ca, kind, namespace, runtimeBinary string
	phase                                                      string
	runtimeArgs                                                []string
}

type artifactServerProcess struct {
	command *exec.Cmd
	done    chan error
	stderr  bytes.Buffer
	stopped bool
}

func newArtifactProcessFixture(
	t *testing.T,
	binary string,
) *artifactProcessFixture {
	t.Helper()
	f := newArtifactE2E(t)
	p := &artifactProcessFixture{
		api: f, binary: binary, root: t.TempDir(),
		kind:      os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME"),
		namespace: os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE"),
	}
	if err := f.h.repo.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").
		Scan(&p.database); err != nil {
		t.Fatal(err)
	}
	response, err := f.h.repo.ArtifactClient.HTTP.Get(f.upstream)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	p.ca = filepath.Join(p.root, "upstream-ca.pem")
	certificate := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: response.TLS.PeerCertificates[0].Raw,
		},
	)
	if err := os.WriteFile(p.ca, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	p.runtimeBinary, err = exec.LookPath(p.kind)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv("DURPDEPLOY_CONTAINER_URL")
	if endpoint == "" {
		if p.kind == "docker" {
			endpoint = os.Getenv("DOCKER_HOST")
		} else {
			endpoint = os.Getenv("CONTAINER_HOST")
		}
	}
	if endpoint != "" {
		p.runtimeArgs = []string{"--host=" + endpoint}
		if p.kind == "podman" {
			p.runtimeArgs = []string{"--remote", "--url=" + endpoint}
		}
	}
	if err := os.MkdirAll(p.workspace(), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *artifactProcessFixture) workspace() string {
	return filepath.Join(p.root, "tmp", "durpdeploy-artifacts")
}

func (p *artifactProcessFixture) start(t *testing.T) *artifactServerProcess {
	t.Helper()
	command := exec.CommandContext(t.Context(), p.binary)
	command.Dir = p.root
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = append(
		os.Environ(),
		"DURPDEPLOY_DB="+p.database+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		"DURPDEPLOY_SECRET_KEY="+base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_ADDR=127.0.0.1:0",
		"DURPDEPLOY_URL=http://localhost",
		"DURPDEPLOY_AGENT_LISTEN_ADDR=127.0.0.1:0",
		"DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost",
		"DURPDEPLOY_AGENT_IDENTITY_DIR="+filepath.Join(
			p.root,
			"agent-identity",
		),
		"DURPDEPLOY_EXECUTION_BOUNDARY=service",
		"TMPDIR="+filepath.Join(p.root, "tmp"),
		"SSL_CERT_FILE="+p.ca,
		"PATH="+filepath.Join(
			p.root,
			"runtime-bin",
		)+string(
			os.PathListSeparator,
		)+os.Getenv(
			"PATH",
		),
		"DURPDEPLOY_ARTIFACT_TEST_PHASE="+p.phase,
		"DURPDEPLOY_ARTIFACT_TEST_EXTRACTION_READY="+filepath.Join(
			p.root,
			"extraction-ready",
		),
		"DURPDEPLOY_ARTIFACT_TEST_EXTRACTION_RELEASE="+filepath.Join(
			p.root,
			"extraction-release",
		),
		"DURPDEPLOY_ARTIFACT_TEST_GATE="+filepath.Join(p.root, "stage-ready"),
		"DURPDEPLOY_ARTIFACT_TEST_RELEASE="+filepath.Join(
			p.root,
			"stage-release",
		),
	)
	process := &artifactServerProcess{
		command: command,
		done:    make(chan error, 1),
	}
	command.Stderr = &process.stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { process.kill(t) })
	ready := make(chan string, 1)
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"addr"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil &&
				event.Message == "server starting" {
				ready <- "http://" + event.Address
			}
		}
	}()
	go func() { process.done <- command.Wait() }()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	select {
	case address := <-ready:
		p.api.baseURL = address
		p.api.api(t, "GET", "/api/v1/healthz", nil, 200)
	case err := <-process.done:
		process.stopped = true
		<-scanned
		t.Fatalf(
			"test server exited before readiness: %v\n%s",
			err,
			process.stderr.String(),
		)
	case <-ctx.Done():
		process.kill(t)
		t.Fatalf(
			"test server readiness: %v\n%s",
			ctx.Err(),
			process.stderr.String(),
		)
	}
	return process
}

func (p *artifactServerProcess) pause(t *testing.T) {
	t.Helper()
	if err := p.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	var info unix.Siginfo
	if err := unix.Waitid(
		unix.P_PID,
		p.command.Process.Pid,
		&info,
		unix.WSTOPPED|unix.WNOWAIT,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}

func (p *artifactServerProcess) kill(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	if err := syscall.Kill(
		-p.command.Process.Pid,
		syscall.SIGKILL,
	); err != nil &&
		err != syscall.ESRCH {
		t.Error(err)
	}
	select {
	case <-p.done:
		p.stopped = true
	case <-time.After(10 * time.Second):
		t.Error(fmt.Errorf("test server process did not terminate"))
	}
}
