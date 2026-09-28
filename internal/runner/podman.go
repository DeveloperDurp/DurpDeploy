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
)

type podmanEndpoint struct {
	url       string
	namespace string
}

var rootlessSocket = regexp.MustCompile(
	`^/run/user/[1-9][0-9]*/podman/podman\.sock$`,
)
var podmanNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func newPodmanEndpoint() (podmanEndpoint, error) {
	raw := os.Getenv("DURPDEPLOY_PODMAN_URL")
	namespace := os.Getenv("DURPDEPLOY_PODMAN_NAMESPACE")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ssh" || u.User == nil ||
		u.User.Username() == "" || u.User.Username() == "root" ||
		u.User.String() != u.User.Username() || u.Hostname() == "" ||
		!rootlessSocket.MatchString(
			u.Path,
		) || u.RawQuery != "" || u.Fragment != "" ||
		!podmanNamespace.MatchString(namespace) {
		return podmanEndpoint{}, fmt.Errorf(
			"configure a dedicated rootless ssh://user@host/run/user/UID/podman/podman.sock endpoint and DURPDEPLOY_PODMAN_NAMESPACE",
		)
	}
	return podmanEndpoint{url: raw, namespace: namespace}, nil
}

func (p podmanEndpoint) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "podman", append([]string{
		"--remote", "--url=" + p.url,
	}, args...)...)
	// Do not let inherited CONTAINER_HOST or Podman configuration redirect work.
	cmd.Env = append(os.Environ(), "CONTAINER_HOST="+p.url)
	return cmd
}

func (r *DeploymentRunner) reconcileAttempts() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := r.podman.command(ctx, "info", "--format=json").Output()
	if err != nil {
		return fmt.Errorf("check remote Podman: %w", err)
	}
	var info struct {
		Host struct {
			Security struct {
				Rootless bool `json:"rootless"`
			} `json:"security"`
		} `json:"host"`
	}
	if err := json.Unmarshal(
		output,
		&info,
	); err != nil ||
		!info.Host.Security.Rootless {
		return fmt.Errorf("Podman execution engine must report rootless mode")
	}
	output, err = r.podman.command(ctx, "ps", "--all", "--quiet",
		"--filter=label=io.durpdeploy.namespace="+r.podman.namespace).Output()
	if err != nil {
		return fmt.Errorf("list orphaned attempts: %w", err)
	}
	for _, id := range strings.Fields(string(output)) {
		if output, err := r.podman.command(ctx, "rm", "--force", "--time=0", "--ignore", id).
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
		if _, err := r.repo.Queries.ConfirmContainerCleanup(ctx, sql.NullInt64{
			Int64: time.Now().Unix(), Valid: true,
		}); err != nil {
			return fmt.Errorf("confirm container cleanup: %w", err)
		}
	}
	return nil
}
