//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setPgid configures cmd to run in its own process group so that
// killProcessGroup can kill the whole tree (bash + anything it spawned)
// instead of just the bash PID, which otherwise leaves grandchildren
// orphaned (P1-3).
func setPgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the given process group. pgid must be a
// positive, real process group ID: syscall.Kill treats a negative pgid
// argument of 0 or less as "kill(-0)"/"kill(-1)", which targets every
// process in the caller's group (or, for -1, every process the caller
// is allowed to signal) — i.e. it can kill the server itself. Callers
// must guard against an unset/zero pgid before calling this.
func killProcessGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
