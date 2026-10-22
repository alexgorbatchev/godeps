package godeps

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fakeTool is a dependency whose reported version changes when it is installed
// or updated, recording which installer method ran.
type fakeTool struct {
	version    string
	newVersion string
	failure    error
	calls      []string
}

func (f *fakeTool) Install(context.Context, string) error { return f.apply("install") }
func (f *fakeTool) Update(context.Context, string) error  { return f.apply("update") }

func (f *fakeTool) apply(call string) error {
	f.calls = append(f.calls, call)
	if f.failure != nil {
		return f.failure
	}
	f.version = f.newVersion
	return nil
}

func (f *fakeTool) run(context.Context, string, ...string) ([]byte, error) {
	if f.version == "" {
		return nil, &exec.Error{Name: "toolA", Err: exec.ErrNotFound}
	}
	return []byte(f.version), nil
}

func (f *fakeTool) manager() *Manager {
	return New(Config{AppName: "testapp", Runner: f.run, Dependencies: []Dependency{{Name: "toolA", MinVersion: "1.4.0", Installer: f}}})
}

func TestEnsure(t *testing.T) {
	tests := []struct {
		name      string
		tool      fakeTool
		options   EnsureOptions
		agent     bool
		wantErr   string
		wantCalls string
		wantOut   string
	}{
		{name: "satisfied", tool: fakeTool{version: "2.0.0"}},
		{name: "auto-installs a missing tool", tool: fakeTool{newVersion: "1.4.0"}, options: EnsureOptions{AutoInstall: true}, wantCalls: "install", wantOut: "Successfully installed: toolA"},
		{name: "auto-updates an outdated tool", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, options: EnsureOptions{AutoInstall: true}, wantCalls: "update", wantOut: "Successfully installed: toolA"},
		{name: "reports a failed installer", tool: fakeTool{version: "1.0.0", failure: errors.New("download failed")}, options: EnsureOptions{AutoInstall: true}, wantErr: "download failed", wantCalls: "update"},
		{name: "rejects an installation that is still outdated", tool: fakeTool{version: "1.0.0", newVersion: "1.1.0"}, options: EnsureOptions{AutoInstall: true}, wantErr: "remain unsatisfied", wantCalls: "update"},
		{name: "agent mode never prompts", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, agent: true, wantErr: "outdated"},
		{name: "prompt accepted", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, options: EnsureOptions{Input: strings.NewReader("y\n")}, wantCalls: "update", wantOut: "[y/N]"},
		{name: "prompt declined by default", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, options: EnsureOptions{Input: strings.NewReader("\n")}, wantErr: "outdated", wantOut: "[y/N]"},
		{name: "prompt declined", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, options: EnsureOptions{Input: strings.NewReader("n\n")}, wantErr: "outdated"},
		{name: "prompt without an answer", tool: fakeTool{version: "1.0.0", newVersion: "1.5.0"}, options: EnsureOptions{Input: strings.NewReader("")}, wantErr: "outdated"},
		{name: "accepted prompt with a failed installer", tool: fakeTool{failure: errors.New("download failed")}, options: EnsureOptions{Input: strings.NewReader("yes\n")}, wantErr: "download failed", wantCalls: "install"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("PATH", os.Getenv("PATH"))
			t.Setenv("AGENT", map[bool]string{false: "0", true: "1"}[tt.agent])
			var out bytes.Buffer
			tt.options.Output = &out
			err := tt.tool.manager().Ensure(context.Background(), tt.options)
			if (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err=%v, want %q", err, tt.wantErr)
			}
			if calls := strings.Join(tt.tool.calls, ","); calls != tt.wantCalls {
				t.Errorf("installer calls %q, want %q", calls, tt.wantCalls)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output %q, want %q", out.String(), tt.wantOut)
			}
		})
	}
}

// Programs that print machine-readable results to standard output must not
// receive installation messages there.
func TestEnsureWritesMessagesToStandardError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", os.Getenv("PATH"))
	stdout := capture(t, &os.Stdout)
	stderr := capture(t, &os.Stderr)
	tool := fakeTool{newVersion: "1.4.0"}
	err := tool.manager().Ensure(context.Background(), EnsureOptions{AutoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := stdout(); got != "" {
		t.Errorf("standard output %q", got)
	}
	if got := stderr(); !strings.Contains(got, "Successfully installed: toolA") {
		t.Errorf("standard error %q", got)
	}
}

// capture replaces a standard stream with a pipe and returns its contents.
func capture(t *testing.T, stream **os.File) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := *stream
	*stream = w
	t.Cleanup(func() { *stream = original })
	return func() string {
		*stream = original
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func TestInstallUnsatisfiedUpdatesInstalledTools(t *testing.T) {
	tests := []struct {
		name      string
		tool      fakeTool
		wantCalls string
	}{
		{name: "missing", tool: fakeTool{newVersion: "1.4.0"}, wantCalls: "install"},
		{name: "outdated", tool: fakeTool{version: "1.0.0", newVersion: "1.4.0"}, wantCalls: "update"},
		{name: "satisfied", tool: fakeTool{version: "1.4.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("PATH", os.Getenv("PATH"))
			installed, err := tt.tool.manager().InstallUnsatisfied(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if calls := strings.Join(tt.tool.calls, ","); calls != tt.wantCalls || len(installed) != len(tt.tool.calls) {
				t.Errorf("calls %q installed %v, want %q", calls, installed, tt.wantCalls)
			}
		})
	}
}
