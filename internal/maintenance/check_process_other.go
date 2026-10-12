//go:build !unix

package maintenance

import "os/exec"

func configureCheckProcess(cmd *exec.Cmd) {
	// Non-Unix platforms retain CommandContext's process cancellation.
}
