//go:build linux

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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

// cgroupRoot is the parent cgroup v2 directory that docs/deploy.md Step 5
// asks the operator to create and chown to the durpdeploy user. Each
// deployment gets its own sub-cgroup under here for the lifetime of the run.
const cgroupRoot = "/sys/fs/cgroup/durpdeploy"

// Default per-deployment resource limits (P1-4). Not currently
// user-configurable — a fixed ceiling is simpler and enough to stop one
// runaway step from starving the host.
const (
	cgroupMemoryMax = "268435456" // 256MB
	cgroupPidsMax   = "100"
	cgroupCPUMax    = "50000 100000" // 50% of one core
)

// chrootBinds are the host paths bind-mounted read-only into every step's
// scratch chroot so bash and its usual toolchain (coreutils, etc.) are
// available inside it. Anything not listed here (the DB, secret key,
// other projects' scratch dirs, ...) simply doesn't exist inside the
// chroot.
var chrootBinds = []string{"/bin", "/usr", "/lib"}

// optionalChrootBinds are mounted when present. Alpine does not provide
// /lib64, while glibc-based systems may need it for dynamic binaries.
var optionalChrootBinds = []string{"/lib64"}

// Sandbox resolves the runner UID/GID once at startup and applies
// per-step isolation (credential drop, cgroup limits, chroot) to each
// step's exec.Cmd.
type Sandbox struct {
	uid                 uint32
	gid                 uint32
	enabled             bool
	applyCredentialFn   func(*exec.Cmd)
	clearCapabilitiesFn func(*exec.Cmd, bool) error
	createCgroupFn      func(int64) (*cgroup, error)
	configureCgroupFn   func(*exec.Cmd, *cgroup) error
	removeCgroupFn      func(*cgroup) error
	setupChrootFn       func(string) (bool, error)
	cgroupUsable        bool
}

type cgroup struct {
	path string
	dir  *os.File
}

// newSandbox looks up the dedicated durpdeploy-runner account. A missing or
// malformed account is fatal: scripts must never execute as the service user.
func newSandbox() (*Sandbox, error) {
	s := &Sandbox{}
	if info, err := os.Stat(cgroupRoot); err == nil && info.IsDir() {
		s.cgroupUsable = true
	}

	u, err := lookupRunnerUser(runnerUsername)
	if err != nil {
		return nil, fmt.Errorf("lookup runner user %q: %w", runnerUsername, err)
	}
	uid, errUID := strconv.ParseUint(u.Uid, 10, 32)
	gid, errGID := strconv.ParseUint(u.Gid, 10, 32)
	if errUID != nil {
		return nil, fmt.Errorf("parse runner UID %q: %w", u.Uid, errUID)
	}
	if errGID != nil {
		return nil, fmt.Errorf("parse runner GID %q: %w", u.Gid, errGID)
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
	if s.applyCredentialFn != nil {
		s.applyCredentialFn(cmd)
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
func (s *Sandbox) clearCapabilities(cmd *exec.Cmd, chrooted bool) error {
	if s.clearCapabilitiesFn != nil {
		return s.clearCapabilitiesFn(cmd, chrooted)
	}
	if !s.enabled {
		return fmt.Errorf("runner sandbox has no %q identity", runnerUsername)
	}
	setpriv := "/usr/bin/setpriv"
	if !chrooted {
		path, err := lookupSetpriv("setpriv")
		if err != nil {
			return fmt.Errorf("runner sandbox requires setpriv: %w", err)
		}
		setpriv = path
	} else if _, err := os.Stat(setpriv); err != nil {
		return fmt.Errorf("runner sandbox requires %s: %w", setpriv, err)
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

func (s *Sandbox) createCgroup(deploymentID int64) (*cgroup, error) {
	if s.createCgroupFn != nil {
		return s.createCgroupFn(deploymentID)
	}
	if !s.cgroupUsable {
		return nil, fmt.Errorf("runner sandbox requires writable cgroup root %s", cgroupRoot)
	}

	dir := filepath.Join(cgroupRoot, fmt.Sprintf("deploy-%d", deploymentID))
	if err := os.Mkdir(dir, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup %s: %w", dir, err)
	}
	limits := []struct {
		file  string
		value string
	}{
		{"memory.max", cgroupMemoryMax},
		{"pids.max", cgroupPidsMax},
		{"cpu.max", cgroupCPUMax},
	}
	for _, limit := range limits {
		if err := os.WriteFile(
			filepath.Join(dir, limit.file),
			[]byte(limit.value),
			0644,
		); err != nil {
			cleanupErr := os.Remove(dir)
			return nil, errors.Join(
				fmt.Errorf("set %s on %s: %w", limit.file, dir, err),
				cleanupErr,
			)
		}
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		cleanupErr := os.Remove(dir)
		return nil, errors.Join(
			fmt.Errorf("open cgroup %s: %w", dir, err),
			cleanupErr,
		)
	}
	return &cgroup{path: dir, dir: dirFile}, nil
}

func (s *Sandbox) configureCgroup(cmd *exec.Cmd, group *cgroup) error {
	if s.configureCgroupFn != nil {
		return s.configureCgroupFn(cmd, group)
	}
	if group == nil || group.dir == nil {
		return fmt.Errorf("runner sandbox has no cgroup file descriptor")
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(group.dir.Fd())
	return nil
}

// removeCgroup deletes the deployment's cgroup directory once the step has
// exited (a cgroup can only be rmdir'd once it has no live processes).
func (s *Sandbox) removeCgroup(group *cgroup) error {
	if s.removeCgroupFn != nil {
		return s.removeCgroupFn(group)
	}
	if group == nil {
		return nil
	}
	var cleanupErr error
	if group.dir != nil {
		cleanupErr = group.dir.Close()
		group.dir = nil
	}
	if group.path == "" {
		return cleanupErr
	}
	return errors.Join(cleanupErr, os.Remove(group.path))
}

// setupChroot bind-mounts chrootBinds read-only into scratchRoot. Any source,
// mount, or remount failure aborts the deployment rather than exposing the host.
func (s *Sandbox) setupChroot(scratchRoot string) (bool, error) {
	if s.setupChrootFn != nil {
		return s.setupChrootFn(scratchRoot)
	}
	mounted := make([]string, 0, len(chrootBinds)+len(optionalChrootBinds))
	for _, src := range chrootBinds {
		if err := s.mountChrootBind(scratchRoot, src); err != nil {
			s.teardownChroot(scratchRoot)
			return false, err
		}
		mounted = append(mounted, src)
	}
	for _, src := range optionalChrootBinds {
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			s.teardownChroot(scratchRoot)
			return false, fmt.Errorf("inspect optional bind source %s: %w", src, err)
		}
		if err := s.mountChrootBind(scratchRoot, src); err != nil {
			s.teardownChroot(scratchRoot)
			return false, err
		}
		mounted = append(mounted, src)
	}
	return len(mounted) > 0, nil
}

func (s *Sandbox) mountChrootBind(scratchRoot, src string) error {
	dst := filepath.Join(scratchRoot, src)
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("create bind target %s: %w", dst, err)
	}
	if err := syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount %s: %w", src, err)
	}
	if err := syscall.Mount(
		"",
		dst,
		"",
		syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY,
		"",
	); err != nil {
		return fmt.Errorf("remount %s read-only: %w", src, err)
	}
	return nil
}

// applyChroot locks cmd into scratchRoot. Must only be called after
// setupChroot(scratchRoot) returned true. Preserves any SysProcAttr fields
// already set (e.g. setPgid's Setpgid, applyCredential's Credential).
func (s *Sandbox) applyChroot(cmd *exec.Cmd, scratchRoot string) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Chroot = scratchRoot
}

// teardownChroot unmounts everything setupChroot bind-mounted. Safe to
// call even if setupChroot mounted nothing (each Unmount is best-effort).
func (s *Sandbox) teardownChroot(scratchRoot string) {
	for _, src := range append(chrootBinds, optionalChrootBinds...) {
		dst := filepath.Join(scratchRoot, src)
		_ = syscall.Unmount(dst, syscall.MNT_DETACH)
	}
}
