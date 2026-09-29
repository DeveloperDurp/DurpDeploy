package runner

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type containerEndpoint struct {
	kind      string
	binary    string
	url       string
	namespace string
}

var podmanNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func newContainerEndpoint() (containerEndpoint, error) {
	return newContainerEndpointWith("", "")
}

func newContainerEndpointWith(
	kindOverride, binaryOverride string,
) (containerEndpoint, error) {
	rawEnabled := os.Getenv("DURPDEPLOY_EMBEDDED_AGENT_ENABLED")
	if rawEnabled == "" {
		rawEnabled = "true"
	}
	enabled, err := strconv.ParseBool(rawEnabled)
	if err != nil {
		return containerEndpoint{}, fmt.Errorf(
			"DURPDEPLOY_EMBEDDED_AGENT_ENABLED must be true or false",
		)
	}
	if !enabled {
		return containerEndpoint{}, fmt.Errorf("embedded agent is disabled")
	}

	kind := kindOverride
	if kind == "" {
		kind = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	}
	if kind == "" {
		kind = detectContainerRuntime()
	}
	if kind != "docker" && kind != "podman" {
		return containerEndpoint{}, fmt.Errorf(
			"DURPDEPLOY_CONTAINER_RUNTIME must be docker or podman",
		)
	}
	raw := os.Getenv("DURPDEPLOY_CONTAINER_URL")
	namespace := os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
	if raw == "" {
		if kind == "docker" {
			raw = os.Getenv("DOCKER_HOST")
		} else {
			raw = os.Getenv("CONTAINER_HOST")
		}
	}
	if namespace == "" {
		namespace = "durpdeploy"
	}
	if !podmanNamespace.MatchString(namespace) {
		return containerEndpoint{}, fmt.Errorf(
			"DURPDEPLOY_CONTAINER_NAMESPACE contains invalid characters",
		)
	}
	if raw != "" {
		u, parseErr := url.Parse(raw)
		if parseErr != nil || u.Scheme != "unix" || u.Host != "" ||
			u.RawQuery != "" || u.Fragment != "" ||
			!filepath.IsAbs(u.Path) {
			return containerEndpoint{}, fmt.Errorf(
				"DURPDEPLOY_CONTAINER_URL must be an absolute unix socket URL",
			)
		}
	}
	binary := binaryOverride
	if binary == "" {
		binary, err = exec.LookPath(kind)
		if err != nil {
			return containerEndpoint{}, fmt.Errorf(
				"find %s client: %w", kind, err,
			)
		}
	}
	return containerEndpoint{
		kind: kind, binary: binary, url: raw, namespace: namespace,
	}, nil
}

func newPodmanEndpoint() (containerEndpoint, error) {
	return newContainerEndpointWith("podman", "")
}

func detectContainerRuntime() string {
	if os.Getenv("DOCKER_HOST") != "" {
		if _, err := exec.LookPath("docker"); err == nil {
			return "docker"
		}
	}
	if os.Getenv("CONTAINER_HOST") != "" {
		if _, err := exec.LookPath("podman"); err == nil {
			return "podman"
		}
	}
	for _, kind := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(kind); err == nil {
			return kind
		}
	}
	return ""
}

func (p containerEndpoint) command(
	ctx context.Context, args ...string,
) *exec.Cmd {
	clientArgs := []string{}
	hostEnv := ""
	if p.url != "" {
		clientArgs = []string{"--host=" + p.url}
		hostEnv = "DOCKER_HOST=" + p.url
		if p.kind == "podman" {
			clientArgs = []string{"--remote", "--url=" + p.url}
			hostEnv = "CONTAINER_HOST=" + p.url
		}
	}
	cmd := exec.CommandContext(ctx, p.binary, append(clientArgs, args...)...)
	// Do not let inherited client configuration redirect execution.
	cmd.Env = os.Environ()
	if hostEnv != "" {
		cmd.Env = append(cmd.Env, hostEnv)
	}
	return cmd
}

func (p containerEndpoint) removeArgs(name string) []string {
	if p.kind == "docker" {
		return []string{"rm", "--force", "--volumes", name}
	}
	return []string{"rm", "--force", "--time=0", "--ignore", name}
}

func (p containerEndpoint) removalError(output []byte, err error) error {
	if err != nil && p.kind == "docker" &&
		strings.Contains(string(output), "No such container") {
		return nil
	}
	return err
}

func (p containerEndpoint) scope() string {
	return p.kind + ":" + p.namespace
}

// NewWithPodmanBinaryForTest injects a fake client before startup reconciliation.
func NewWithPodmanBinaryForTest(
	repo *repository.Repository, broker *LogBroker, binary string,
) *DeploymentRunner {
	return newRunnerWithKind(repo, broker, "podman", binary)
}

func newRunner(
	repo *repository.Repository, broker *LogBroker, testBinary string,
) *DeploymentRunner {
	return newRunnerWithKind(repo, broker, "", testBinary)
}

func newRunnerWithKind(
	repo *repository.Repository,
	broker *LogBroker,
	kind, testBinary string,
) *DeploymentRunner {
	endpoint, localErr := newContainerEndpointWith(kind, testBinary)
	r := &DeploymentRunner{
		repo:     repo,
		broker:   broker,
		cancels:  make(map[int64]context.CancelFunc),
		attempts: make(map[int64]string),
		engine:   endpoint,
		localErr: localErr,
	}
	if localErr == nil {
		r.localErr = r.reconcileAttempts()
	}
	return r
}

func (r *DeploymentRunner) reconcileAttempts() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := r.engine.command(ctx, "info").CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"check container runtime: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	output, err = r.engine.command(ctx, "ps", "--all", "--quiet",
		"--filter=label=io.durpdeploy.namespace="+r.engine.scope()).Output()
	if err != nil {
		return fmt.Errorf("list orphaned attempts: %w", err)
	}
	for _, id := range strings.Fields(string(output)) {
		if output, err := r.engine.command(ctx, r.engine.removeArgs(id)...).
			CombinedOutput(); err != nil {
			return fmt.Errorf(
				"remove orphaned attempt %s: %w: %s",
				id,
				err,
				strings.TrimSpace(string(output)),
			)
		}
	}
	if r.repo != nil {
		if _, err := r.repo.Queries.ConfirmContainerCleanup(ctx,
			db.ConfirmContainerCleanupParams{
				Now: sql.NullInt64{
					Int64: time.Now().Unix(), Valid: true,
				},
				Namespace: sql.NullString{
					String: r.engine.scope(), Valid: true,
				},
			}); err != nil {
			return fmt.Errorf("confirm container cleanup: %w", err)
		}
	}
	return nil
}
