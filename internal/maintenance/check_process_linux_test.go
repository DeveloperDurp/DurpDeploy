package maintenance

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunCheckCleansOrphanedOutput(t *testing.T) {
	for _, status := range []int{0, 1} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			// Given: a shell exits while a child keeps the output pipe open.
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			command := fmt.Sprintf(
				"sleep 30 & echo $! > '%s'; exit %d",
				pidFile,
				status,
			)
			// When: the output wait budget expires.
			if err := runCheck(t.Context(), command); err == nil {
				t.Fatal("orphaned output should fail the check")
			}
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				t.Fatal("invalid child PID")
			}
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			// Then: the child is gone or waiting to be reaped, never running.
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				state, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
				if os.IsNotExist(err) ||
					strings.Contains(string(state), ") Z ") {
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("check left its child running")
		})
	}
}
