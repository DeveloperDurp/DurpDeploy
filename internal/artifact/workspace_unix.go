//go:build unix

package artifact

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func createWorkspaceDirectory(directory string) error {
	err := os.Mkdir(directory, 0700)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

func validateWorkspaceDirectory(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return errors.New(
			"artifact workspace must be private and owned by the server user",
		)
	}
	return nil
}

func lockWorkspace(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrWorkspaceBusy
	}
	return err
}
