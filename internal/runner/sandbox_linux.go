//go:build linux

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
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
var chrootBinds = []string{"/bin", "/usr", "/lib", "/lib64"}

// Sandbox resolves the runner UID/GID once at startup and applies
// per-step isolation (credential drop, cgroup limits, chroot) to each
// step's exec.Cmd.
type Sandbox struct {
	uid, gid     uint32
	enabled      bool
	warned       atomic.Bool
	cgroupUsable bool
	cgroupWarned atomic.Bool
	chrootWarned atomic.Bool
}

// newSandbox looks up the durpdeploy-runner account. If it does not exist
// (e.g. local dev/CI where docs/deploy.md Step 5 was never run), the
// sandbox is disabled and steps keep running as the server's own user —
// applyCredential logs a one-time warning instead of failing deployments.
func newSandbox() *Sandbox {
	s := &Sandbox{}
	if info, err := os.Stat(cgroupRoot); err == nil && info.IsDir() {
		s.cgroupUsable = true
	}

	u, err := user.Lookup(runnerUsername)
	if err != nil {
		return s
	}
	uid, errUID := strconv.ParseUint(u.Uid, 10, 32)
	gid, errGID := strconv.ParseUint(u.Gid, 10, 32)
	if errUID != nil || errGID != nil {
		return s
	}
	s.uid, s.gid = uint32(uid), uint32(gid)
	s.enabled = true
	return s
}

// applyCredential drops the step process to the durpdeploy-runner UID/GID.
// Preserves any SysProcAttr fields already set (e.g. setPgid's Setpgid).
// NoNewPrivileges is set at the systemd unit level (see
// systemd/durpdeploy.service) since Go's syscall.SysProcAttr does not
// expose a per-Cmd equivalent.
func (s *Sandbox) applyCredential(cmd *exec.Cmd) {
	if !s.enabled {
		if s.warned.CompareAndSwap(false, true) {
			fmt.Fprintf(
				os.Stderr,
				"runner: %q user not found, steps run as the server's own user (see docs/deploy.md Step 5 to enable the sandbox)\n",
				runnerUsername,
			)
		}
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

// createCgroup creates a fresh cgroup v2 directory for this deployment
// under cgroupRoot and writes the default cpu/memory/pids limits. Returns
// "" (no error) if cgroupRoot isn't set up (docs/deploy.md Step 5 wasn't
// followed) — the step then simply runs without resource limits.
func (s *Sandbox) createCgroup(deploymentID int64) string {
	if !s.cgroupUsable {
		if s.cgroupWarned.CompareAndSwap(false, true) {
			fmt.Fprintf(
				os.Stderr,
				"runner: %s not found, steps run without cgroup resource limits (see docs/deploy.md Step 5 to enable)\n",
				cgroupRoot,
			)
		}
		return ""
	}

	dir := filepath.Join(cgroupRoot, fmt.Sprintf("deploy-%d", deploymentID))
	if err := os.Mkdir(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "runner: creating cgroup %s: %v\n", dir, err)
		return ""
	}
	limits := map[string]string{
		"memory.max": cgroupMemoryMax,
		"pids.max":   cgroupPidsMax,
		"cpu.max":    cgroupCPUMax,
	}
	for file, value := range limits {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(value), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "runner: setting %s on %s: %v\n", file, dir, err)
		}
	}
	return dir
}

// addProcess moves pid into the given cgroup. Must be called after
// cmd.Start() (the PID doesn't exist before then). No-op if cgroup is "".
func (s *Sandbox) addProcess(cgroup string, pid int) {
	if cgroup == "" {
		return
	}
	procs := filepath.Join(cgroup, "cgroup.procs")
	if err := os.WriteFile(procs, []byte(strconv.Itoa(pid)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "runner: adding pid %d to cgroup %s: %v\n", pid, cgroup, err)
	}
}

// removeCgroup deletes the deployment's cgroup directory once the step has
// exited (a cgroup can only be rmdir'd once it has no live processes).
// No-op if cgroup is "".
func (s *Sandbox) removeCgroup(cgroup string) {
	if cgroup == "" {
		return
	}
	if err := os.Remove(cgroup); err != nil {
		fmt.Fprintf(os.Stderr, "runner: removing cgroup %s: %v\n", cgroup, err)
	}
}

// setupChroot bind-mounts chrootBinds (read-only, skipping any that don't
// exist on this distro) into scratchRoot. Returns false if it could not
// mount anything (e.g. missing CAP_SYS_ADMIN in local dev/CI), in which
// case the caller should run the step un-chrooted rather than fail the
// deployment.
func (s *Sandbox) setupChroot(scratchRoot string) bool {
	mounted := false
	for _, src := range chrootBinds {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(scratchRoot, src)
		if err := os.MkdirAll(dst, 0755); err != nil {
			continue
		}
		if err := syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			if s.chrootWarned.CompareAndSwap(false, true) {
				fmt.Fprintf(
					os.Stderr,
					"runner: bind-mounting %s: %v (steps run without chroot isolation, see docs/deploy.md Step 5)\n",
					src, err,
				)
			}
			continue
		}
		// Remount read-only; the step must not be able to modify the
		// host's /bin, /usr, /lib, /lib64 through the bind mount.
		_ = syscall.Mount("", dst, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
		mounted = true
	}
	return mounted
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
	for _, src := range chrootBinds {
		dst := filepath.Join(scratchRoot, src)
		_ = syscall.Unmount(dst, syscall.MNT_DETACH)
	}
}
