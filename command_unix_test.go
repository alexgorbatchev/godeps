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
// same, and waits. ignoreChild and ignoreSubprocess make the shell and its
// subprocess keep running after SIGTERM.
func groupScript(ignoreChild, ignoreSubprocess bool) string {
	return `trap 'echo child-term >> "$LOG"; ` + termAction(ignoreChild) + `' TERM
sh -c 'trap "echo grandchild-term >> \"$LOG\"; ` + termAction(ignoreSubprocess) + `" TERM; echo $$ >> "$LOG"; while :; do sleep 0.05; done' &
while :; do sleep 0.05; done`
}

func termAction(ignore bool) string {
	if ignore {
		return ":"
	}
	return "exit 0"
}

func TestCommandContextTerminatesProcessGroupGracefully(t *testing.T) {
	withGracePeriod(t, 5*time.Second)
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(ctx, "sh", "-c", groupScript(false, false))
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
	cmd := CommandContext(ctx, "sh", "-c", groupScript(true, true))
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

// A command that exits on SIGTERM can leave a subprocess behind that ignores
// it. Wait must not return before that subprocess is killed, or a caller that
// exits right away takes the pending SIGKILL with it.
func TestCommandContextWaitOutlastsGroupMembersIgnoringSIGTERM(t *testing.T) {
	withGracePeriod(t, 300*time.Millisecond)
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Leaving Stdout and Stderr unset gives the command no output pipes to wait
	// for, so only the process group can hold Wait back.
	cmd := CommandContext(ctx, "sh", "-c", groupScript(false, true))
	cmd.Env = append(os.Environ(), "LOG="+log)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild, _ := strconv.Atoi(waitForLines(t, log, 1)[0])
	t.Cleanup(func() {
		if processAlive(grandchild) {
			_ = syscall.Kill(grandchild, syscall.SIGKILL)
		}
	})

	start := time.Now()
	cancel()
	if err := cmd.Wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed < terminationGracePeriod {
		t.Errorf("Wait returned after %v, before the %v grace period that the surviving subprocess needed", elapsed, terminationGracePeriod)
	}
	lines := waitForLines(t, log, 3)
	if joined := strings.Join(lines, " "); !strings.Contains(joined, "child-term") || !strings.Contains(joined, "grandchild-term") {
		t.Errorf("log %v, want SIGTERM delivered to the command and its subprocess", lines)
	}
	// SIGKILL was sent before Wait returned; allow only for its delivery.
	deadline := time.Now().Add(200 * time.Millisecond)
	for processAlive(grandchild) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if processAlive(grandchild) {
		t.Errorf("subprocess %d ignoring SIGTERM outlived Wait", grandchild)
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
	if _, err := DefaultRunner(ctx, "sh", "-c", groupScript(false, false)); err == nil {
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

// Package managers may prompt through the terminal, so they stay in the
// caller's process group, and cancellation still sends SIGTERM first.
func TestPackageManagerRunnerStaysInForegroundGroup(t *testing.T) {
	withGracePeriod(t, 5*time.Second)
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("LOG", log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runPackageManager(ctx, "sh", "-c", `trap 'echo term >> "$LOG"; exit 0' TERM; echo $$ >> "$LOG"; while :; do sleep 0.05; done`)
		done <- err
	}()
	pid, _ := strconv.Atoi(waitForLines(t, log, 1)[0])
	group, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if group != syscall.Getpgrp() {
		t.Errorf("package manager runs in process group %d, want the caller's group %d", group, syscall.Getpgrp())
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("runPackageManager succeeded for a cancelled command")
	}
	if lines := waitForLines(t, log, 2); lines[1] != "term" {
		t.Errorf("log %v, want SIGTERM on cancellation", lines)
	}
}
