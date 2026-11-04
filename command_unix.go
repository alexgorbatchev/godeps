//go:build unix

package godeps

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// groupPollInterval is how often a terminated process group is checked for
// members that are still running.
const groupPollInterval = 20 * time.Millisecond

// terminateGracefully starts cmd in its own process group and makes
// cancellation send SIGTERM to the group, then SIGKILL to whatever is left of
// it after grace.
func terminateGracefully(cmd *exec.Cmd, grace time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Setpgid makes the child's process ID its process group ID.
		group := cmd.Process.Pid
		if err := syscall.Kill(-group, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		go killGroupAfter(group, grace)
		return nil
	}
}

// killGroupAfter sends SIGKILL to the process group if any member is still
// running once grace has elapsed. It returns early when the group is gone, so
// it never signals a group ID the system has reused.
func killGroupAfter(group int, grace time.Duration) {
	deadline := time.Now().Add(grace)
	ticker := time.NewTicker(groupPollInterval)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			return
		}
		<-ticker.C
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
}

// requestTermination makes cancellation send SIGTERM to cmd's process; its
// WaitDelay then bounds how long it may take to exit before it is killed.
func requestTermination(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
}
