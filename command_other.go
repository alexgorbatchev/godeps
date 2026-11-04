//go:build !unix

package godeps

import (
	"os/exec"
	"time"
)

// terminateGracefully keeps exec.CommandContext's default cancellation, which
// kills the process: platforms without Unix signals have no termination
// request to send first.
func terminateGracefully(*exec.Cmd, time.Duration) {}

// requestTermination keeps exec.CommandContext's default cancellation, which
// kills the process.
func requestTermination(*exec.Cmd) {}
