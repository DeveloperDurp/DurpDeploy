//go:build linux

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

var (
	lookupRunnerUser = user.Lookup
	lookupSetpriv    = exec.LookPath
)

// runnerUsername is the dedicated, low-privileged account (see docs/deploy.md
// Step 5) that step scripts execute as, instead of the durpdeploy service
// user (P1-4). This keeps a compromised/buggy step from reading the SQLite
// DB, the secret key file, or other files only the service user can access.
const runnerUsername = "durpdeploy-runner"

// Sandbox resolves the runner UID/GID once at startup and applies
// the credential and capability boundary to each step command. Filesystem
// and cgroup boundaries are owned by the service manager or container runtime.
type Sandbox struct {
	uid, gid uint32
	enabled  bool
}

// newSandbox requires an explicit execution mode so production cannot
// accidentally use the development no-op boundary.
func newSandbox() (*Sandbox, error) {
	s := &Sandbox{}
	boundary := os.Getenv("DURPDEPLOY_EXECUTION_BOUNDARY")
	if boundary == "development" {
		return s, nil
	}
	if boundary == "" {
		return nil, fmt.Errorf("runner execution boundary is required")
	}
	if boundary != "service" {
		return nil, fmt.Errorf("invalid runner execution boundary %q", boundary)
	}
	u, err := lookupRunnerUser(runnerUsername)
	if err != nil {
		return nil, fmt.Errorf("lookup runner user %q: %w", runnerUsername, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse runner UID %q: %w", u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse runner GID %q: %w", u.Gid, err)
	}
	s.uid, s.gid = uint32(uid), uint32(gid)
	s.enabled = true
	return s, nil
}

// applyCredential drops the step process to the durpdeploy-runner UID/GID.
// Preserves any SysProcAttr fields already set (e.g. setPgid's Setpgid).
// NoNewPrivileges is set at the systemd unit level (see
// systemd/durpdeploy.service) since Go's syscall.SysProcAttr does not
// expose a per-Cmd equivalent.
func (s *Sandbox) applyCredential(cmd *exec.Cmd) {
	if !s.enabled {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    s.uid,
		Gid:    s.gid,
		Groups: []uint32{s.gid},
	}
}

// clearCapabilities makes setpriv the immediate child and has it remove every
// inherited capability before it execs the attacker-controlled step. Changing
// UID alone is insufficient when the service has ambient capabilities.
func (s *Sandbox) clearCapabilities(cmd *exec.Cmd) error {
	if !s.enabled {
		return nil
	}
	setpriv, err := lookupSetpriv("setpriv")
	if err != nil {
		return fmt.Errorf("runner sandbox requires setpriv: %w", err)
	}
	args := append([]string{
		"setpriv",
		"--bounding-set=-all",
		"--inh-caps=-all",
		"--ambient-caps=-all",
		"--no-new-privs",
		"--",
	}, cmd.Args...)
	cmd.Path = setpriv
	cmd.Args = args
	return nil
}
