//go:build unix

package godeps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func withGracePeriod(t *testing.T, d time.Duration) {
	t.Helper()
	old := terminationGracePeriod
	terminationGracePeriod = d
	t.Cleanup(func() { terminationGracePeriod = old })
}

// pollLines polls path for up to five seconds until it holds at least n lines,
// and returns the lines it last read.
func pollLines(path string, n int) []string {
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		lines := strings.Fields(string(data))
		if len(lines) >= n || time.Now().After(deadline) {
			return lines
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForLines(t *testing.T, path string, n int) []string {
	t.Helper()
	lines := pollLines(path, n)
	if len(lines) < n {
		t.Fatalf("%s holds %q, want %d lines", path, lines, n)
	}
	return lines
}

func processAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// A shell that records SIGTERM in $LOG, starts a subprocess that does the
// same, and waits. ignore makes both keep running after SIGTERM.
func groupScript(ignore bool) string {
	onTerm := "exit 0"
	if ignore {
		onTerm = ":"
	}
	return `trap 'echo child-term >> "$LOG"; ` + onTerm + `' TERM
sh -c 'trap "echo grandchild-term >> \"$LOG\"; ` + onTerm + `" TERM; echo $$ >> "$LOG"; while :; do sleep 0.05; done' &
while :; do sleep 0.05; done`
}

func TestCommandContextTerminatesProcessGroupGracefully(t *testing.T) {
	withGracePeriod(t, 5*time.Second)
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(ctx, "sh", "-c", groupScript(false))
	cmd.Env = append(os.Environ(), "LOG="+log)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild, _ := strconv.Atoi(waitForLines(t, log, 1)[0])

	start := time.Now()
	cancel()
	err := cmd.Wait()
	if elapsed := time.Since(start); elapsed >= terminationGracePeriod {
		t.Errorf("Wait took %v; a command that exits on SIGTERM must not wait for the grace period", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() = %v, want context.Canceled", err)
	}
	lines := waitForLines(t, log, 3)
	if !strings.Contains(strings.Join(lines, " "), "child-term") || !strings.Contains(strings.Join(lines, " "), "grandchild-term") {
		t.Errorf("log %v, want SIGTERM delivered to the command and its subprocess", lines)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(grandchild) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(grandchild) {
		t.Errorf("subprocess %d survived SIGTERM", grandchild)
	}
}

func TestCommandContextKillsProcessGroupAfterGracePeriod(t *testing.T) {
	withGracePeriod(t, 300*time.Millisecond)
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(ctx, "sh", "-c", groupScript(true))
	cmd.Env = append(os.Environ(), "LOG="+log)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild, _ := strconv.Atoi(waitForLines(t, log, 1)[0])

	start := time.Now()
	cancel()
	err := cmd.Wait()
	if elapsed := time.Since(start); elapsed < terminationGracePeriod {
		t.Errorf("Wait returned after %v, before the %v grace period", elapsed, terminationGracePeriod)
	}
	if err == nil {
		t.Error("Wait() succeeded for a killed command")
	}
	// SIGTERM reached both processes before SIGKILL.
	lines := waitForLines(t, log, 3)
	if !strings.Contains(strings.Join(lines, " "), "child-term") || !strings.Contains(strings.Join(lines, " "), "grandchild-term") {
		t.Errorf("log %v, want SIGTERM before SIGKILL", lines)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(grandchild) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(grandchild) {
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Errorf("subprocess %d ignoring SIGTERM survived the grace period", grandchild)
	}
}

func TestDefaultRunnerTerminatesGracefully(t *testing.T) {
	withGracePeriod(t, 5*time.Second)
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("LOG", log)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		pollLines(log, 1)
		cancel()
	}()
	if _, err := DefaultRunner(ctx, "sh", "-c", groupScript(false)); err == nil {
		t.Fatal("DefaultRunner succeeded for a cancelled command")
	}
	if lines := waitForLines(t, log, 3); !strings.Contains(strings.Join(lines, " "), "child-term") {
		t.Errorf("log %v, want SIGTERM delivered through DefaultRunner", lines)
	}
}

func TestCancelAfterExitReportsProcessDone(t *testing.T) {
	cmd := CommandContext(context.Background(), "true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Cancel() after exit = %v, want os.ErrProcessDone", err)
	}
}
