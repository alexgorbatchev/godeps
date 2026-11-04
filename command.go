package godeps

import (
	"context"
	"os/exec"
	"time"
)

// terminationGracePeriod bounds how long a cancelled command may take to exit
// after it is asked to terminate before it is killed.
var terminationGracePeriod = 5 * time.Second

// CommandContext is exec.CommandContext for non-interactive tools that must be
// allowed to clean up when ctx is done.
//
// On Unix, the command runs in its own process group. Cancellation sends
// SIGTERM to the whole group and SIGKILL only to members still running after a
// grace period, so a tool's own subprocesses stop too and a tool such as a
// PyInstaller build can remove its temporary files. Because the group is not
// the terminal's foreground group, a terminal interrupt does not reach the
// command directly: cancel ctx on interrupt, for example with
// signal.NotifyContext.
//
// On Windows, which has no SIGTERM, cancellation kills the process as
// exec.CommandContext does.
//
// On every platform, Wait gives up on output held open by orphaned
// subprocesses after the same grace period.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = terminationGracePeriod
	terminateGracefully(cmd, terminationGracePeriod)
	return cmd
}

// foregroundCommandContext is exec.CommandContext for a command that may need
// the terminal. The command stays in the caller's process group, so it can read
// from the terminal and receives terminal interrupts directly. Cancellation
// asks it to terminate (SIGTERM on Unix) and kills it after the grace period.
func foregroundCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = terminationGracePeriod
	requestTermination(cmd)
	return cmd
}
