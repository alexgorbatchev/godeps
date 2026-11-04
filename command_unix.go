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
// it after grace. Cancel returns only once the group is gone or has been sent
// SIGKILL, and Wait does not return before Cancel does, so a caller that exits
// as soon as Wait returns leaves nothing running.
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
		stopGroup(group, grace)
		return nil
	}
}

// stopGroup waits up to grace for every member of the process group to exit,
// then sends SIGKILL to those still running. It returns as soon as the group
// is gone, which limits the chance of signaling a reused group ID to the one
// poll interval before the SIGKILL.
func stopGroup(group int, grace time.Duration) {
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
