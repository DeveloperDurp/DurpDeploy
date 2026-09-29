package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
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

var rootlessSocket = regexp.MustCompile(
	`^/run/user/[1-9][0-9]*/podman/podman\.sock$`,
)
var rootlessDockerSocket = regexp.MustCompile(
	`^/run/user/[1-9][0-9]*/docker\.sock$`,
)
var podmanNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func newContainerEndpoint() (containerEndpoint, error) {
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if kind == "" {
		kind = "podman"
	}
	raw := os.Getenv("DURPDEPLOY_CONTAINER_URL")
	namespace := os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
	// Accept the Podman-specific names used by earlier configurations.
	if kind == "podman" && raw == "" {
		raw = os.Getenv("DURPDEPLOY_PODMAN_URL")
	}
	if kind == "podman" && namespace == "" {
		namespace = os.Getenv("DURPDEPLOY_PODMAN_NAMESPACE")
	}
	u, err := url.Parse(raw)
	validSSH := err == nil && u.Scheme == "ssh" && u.User != nil &&
		u.User.Username() != "" && u.User.Username() != "root" &&
		u.User.String() == u.User.Username() && u.Hostname() != "" &&
		u.RawQuery == "" && u.Fragment == ""
	if !validSSH || !podmanNamespace.MatchString(namespace) {
		return containerEndpoint{}, fmt.Errorf(
			"configure a dedicated rootless Docker or Podman SSH endpoint and DURPDEPLOY_CONTAINER_NAMESPACE",
		)
	}
	switch kind {
	case "podman":
		if !rootlessSocket.MatchString(u.Path) {
			return containerEndpoint{}, fmt.Errorf(
				"Podman URL must select /run/user/UID/podman/podman.sock",
			)
		}
		return containerEndpoint{kind: kind, binary: "/usr/bin/podman",
			url: raw, namespace: namespace}, nil
	case "docker":
		if !rootlessDockerSocket.MatchString(u.Path) {
			return containerEndpoint{}, fmt.Errorf(
				"Docker URL must select /run/user/UID/docker.sock",
			)
		}
		return containerEndpoint{kind: kind, binary: "/usr/bin/docker",
			url: raw, namespace: namespace}, nil
	default:
		return containerEndpoint{}, fmt.Errorf(
			"DURPDEPLOY_CONTAINER_RUNTIME must be docker or podman",
		)
	}
}

func newPodmanEndpoint() (containerEndpoint, error) {
	return newContainerEndpoint()
}

func (p containerEndpoint) command(
	ctx context.Context, args ...string,
) *exec.Cmd {
	clientArgs := []string{"--host=" + p.url}
	hostEnv := "DOCKER_HOST=" + p.url
	if p.kind == "podman" {
		clientArgs = []string{"--remote", "--ssh=native", "--url=" + p.url}
		hostEnv = "CONTAINER_HOST=" + p.url
	}
	cmd := exec.CommandContext(ctx, p.binary, append(clientArgs, args...)...)
	// Do not let inherited client configuration redirect execution.
	cmd.Env = append(os.Environ(), hostEnv)
	if p.kind == "podman" {
		cmd.Env = append(cmd.Env, "CONTAINER_SSHKEY="+
			os.Getenv("HOME")+"/.ssh/id_ed25519")
	}
	return cmd
}

func (p containerEndpoint) removeArgs(name string) []string {
	if p.kind == "docker" {
		return []string{"rm", "--force", name}
	}
	return []string{"rm", "--force", "--time=0", "--ignore", name}
}

func (p containerEndpoint) scope() string {
	return p.kind + ":" + p.namespace
}

// NewWithPodmanBinaryForTest injects a fake client before startup reconciliation.
func NewWithPodmanBinaryForTest(
	repo *repository.Repository, broker *LogBroker, binary string,
) *DeploymentRunner {
	return newRunner(repo, broker, binary)
}

func newRunner(
	repo *repository.Repository, broker *LogBroker, testBinary string,
) *DeploymentRunner {
	endpoint, localErr := newContainerEndpoint()
	if testBinary != "" {
		endpoint.binary = testBinary
	}
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
	infoArgs := []string{"info", "--format=json"}
	if r.engine.kind == "docker" {
		infoArgs = []string{"info", "--format={{json .SecurityOptions}}"}
	}
	output, err := r.engine.command(ctx, infoArgs...).Output()
	if err != nil {
		return fmt.Errorf("check remote container runtime: %w", err)
	}
	rootless := false
	if r.engine.kind == "docker" {
		var options []string
		if json.Unmarshal(output, &options) == nil {
			for _, option := range options {
				rootless = rootless || option == "name=rootless"
			}
		}
	} else {
		var info struct {
			Host struct {
				Security struct {
					Rootless bool `json:"rootless"`
				} `json:"security"`
			} `json:"host"`
		}
		if json.Unmarshal(output, &info) == nil {
			rootless = info.Host.Security.Rootless
		}
	}
	if !rootless {
		return fmt.Errorf("container runtime must report rootless mode")
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
