package godeps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

type memoryCache struct {
	mu   sync.RWMutex
	data map[string]string
}

func (m *memoryCache) Get(key string, target any) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[key]
	if !ok {
		return false
	}
	if strPtr, ok := target.(*string); ok {
		*strPtr = val
		return true
	}
	return false
}

func (m *memoryCache) Put(key string, val any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if str, ok := val.(string); ok {
		m.data[key] = str
	}
	return nil
}

func (m *memoryCache) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func TestIsAgentMode(t *testing.T) {
	os.Unsetenv("AGENT")
	if IsAgentMode() {
		t.Errorf("IsAgentMode() = true, want false when AGENT is unset")
	}

	os.Setenv("AGENT", "1")
	if !IsAgentMode() {
		t.Errorf("IsAgentMode() = false, want true when AGENT=1")
	}

	os.Setenv("AGENT", "true")
	if !IsAgentMode() {
		t.Errorf("IsAgentMode() = false, want true when AGENT=true")
	}

	os.Setenv("AGENT", "0")
	if IsAgentMode() {
		t.Errorf("IsAgentMode() = true, want false when AGENT=0")
	}

	os.Unsetenv("AGENT")
}

func TestDependencyReport_Summary(t *testing.T) {
	tests := []struct {
		name   string
		report DependencyReport
		want   string
	}{
		{
			name: "satisfied",
			report: DependencyReport{
				Name:            "fetch-track",
				MinVersion:      "1.4.0",
				DetectedVersion: "1.4.0",
				Installed:       true,
				Satisfied:       true,
			},
			want: "fetch-track (version 1.4.0, min 1.4.0)",
		},
		{
			name: "installed but outdated",
			report: DependencyReport{
				Name:            "fetch-track",
				MinVersion:      "1.4.0",
				DetectedVersion: "1.3.0",
				Installed:       true,
				Satisfied:       false,
			},
			want: "fetch-track (installed 1.3.0, required >= 1.4.0)",
		},
		{
			name: "not installed",
			report: DependencyReport{
				Name:       "yt-dlp",
				MinVersion: "2024.08.01",
				Installed:  false,
				Satisfied:  false,
			},
			want: "yt-dlp (not installed, required >= 2024.08.01)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.report.Summary()
			if got != tt.want {
				t.Errorf("DependencyReport.Summary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	mgr := New(Config{})
	if mgr.appName != "godeps" {
		t.Errorf("expected default appName 'godeps', got %q", mgr.appName)
	}
	if mgr.runner == nil {
		t.Errorf("expected default runner not nil")
	}
}

func TestManager_Verify(t *testing.T) {
	ctx := context.Background()

	// 1. All satisfied
	okRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "toolA":
			return []byte("1.4.0"), nil
		case "toolB":
			return []byte("2025.01.01"), nil
		}
		return nil, errors.New("unknown")
	}

	mgr := New(Config{
		AppName: "testapp",
		Dependencies: []Dependency{
			{Name: "toolA", MinVersion: "1.0.0"},
			{Name: "toolB", MinVersion: "2024.01.01"},
		},
		Runner: okRunner,
	})

	reports, err := mgr.Verify(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(reports) != 2 || !reports[0].Satisfied || !reports[1].Satisfied {
		t.Errorf("expected all reports satisfied, got %#v", reports)
	}

	// 2. Missing binary (not found) in agent mode
	t.Setenv("AGENT", "1")
	missingRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	mgr.runner = missingRunner
	reports, err = mgr.Verify(ctx)
	if err == nil {
		t.Errorf("expected error for missing binaries in agent mode")
	}

	// 2b. Missing binary in non-agent mode
	t.Setenv("AGENT", "0")
	reports, err = mgr.Verify(ctx)
	if err == nil {
		t.Errorf("expected error for missing binaries in non-agent mode")
	}

	// 3. Outdated binary in agent mode
	t.Setenv("AGENT", "1")
	outdatedRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("0.0.1"), nil
	}
	mgr.runner = outdatedRunner
	reports, err = mgr.Verify(ctx)
	if err == nil {
		t.Errorf("expected error for outdated binary in agent mode")
	}

	// 3b. Outdated binary in non-agent mode
	t.Setenv("AGENT", "0")
	reports, err = mgr.Verify(ctx)
	if err == nil {
		t.Errorf("expected error for outdated binary in non-agent mode")
	}

	// 4. Execution error
	errRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("execution failed")
	}
	mgr.runner = errRunner
	_, err = mgr.Verify(ctx)
	if err == nil {
		t.Errorf("expected error for runner failure")
	}

	// 5. Empty version output
	emptyRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(""), nil
	}
	mgr.runner = emptyRunner
	_, _ = mgr.Verify(ctx)
}

func TestManager_Verify_WithCache(t *testing.T) {
	ctx := context.Background()
	c := &memoryCache{data: map[string]string{
		"godeps_toolA.json": "2.0.0",
		"godeps_toolB.json": "2025.01.01",
	}}

	failRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("runner should not be called when cache is populated for %s", name)
		return nil, errors.New("fail")
	}

	mgr := New(Config{
		AppName: "testapp",
		Dependencies: []Dependency{
			{Name: "toolA", MinVersion: "1.0.0"},
			{Name: "toolB", MinVersion: "2024.01.01"},
		},
		Runner: failRunner,
		Cache:  c,
	})

	reports, err := mgr.Verify(ctx)
	if err != nil {
		t.Fatalf("unexpected error with cache: %v", err)
	}
	if len(reports) != 2 || !reports[0].Satisfied {
		t.Errorf("expected satisfied cached reports, got %#v", reports)
	}
}

func TestManager_InstallAndUpdate(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tempDir)

	installedMap := make(map[string]bool)

	mockInstaller := InstallerFunc(func(ctx context.Context, targetDir string) error {
		installedMap["toolA"] = true
		return nil
	})

	failingInstaller := InstallerFunc(func(ctx context.Context, targetDir string) error {
		return errors.New("install failure")
	})

	c := &memoryCache{data: map[string]string{"godeps_toolA.json": "old"}}

	mgr := New(Config{
		AppName: "testapp",
		Dependencies: []Dependency{
			{
				Name:       "toolA",
				MinVersion: "1.0.0",
				Installer:  mockInstaller,
			},
			{
				Name:       "toolNoInstaller",
				MinVersion: "1.0.0",
			},
			{
				Name:       "toolFail",
				MinVersion: "1.0.0",
				Installer:  failingInstaller,
			},
		},
		Runner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if installedMap[name] {
				return []byte("1.0.0"), nil
			}
			return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
		},
		Cache: c,
	})

	// Install single
	if err := mgr.Install(ctx, "toolA"); err != nil {
		t.Fatalf("mgr.Install failed: %v", err)
	}
	if !installedMap["toolA"] {
		t.Errorf("toolA was not installed")
	}

	// Update single
	if err := mgr.Update(ctx, "toolA"); err != nil {
		t.Fatalf("mgr.Update failed: %v", err)
	}

	// Install unknown
	if err := mgr.Install(ctx, "unknown"); err == nil {
		t.Errorf("expected error for unknown dependency install")
	}

	// Update unknown
	if err := mgr.Update(ctx, "unknown"); err == nil {
		t.Errorf("expected error for unknown dependency update")
	}

	// Install without installer
	if err := mgr.Install(ctx, "toolNoInstaller"); err == nil {
		t.Errorf("expected error for dependency with nil installer")
	}

	// Update without installer
	if err := mgr.Update(ctx, "toolNoInstaller"); err == nil {
		t.Errorf("expected error for dependency update with nil installer")
	}

	// Install with empty appName (fails EnsureManagedBinDir)
	mgrEmptyApp := &Manager{appName: "", dependencies: []Dependency{{Name: "toolA", Installer: mockInstaller}}}
	if err := mgrEmptyApp.Install(ctx, "toolA"); err == nil {
		t.Errorf("expected error when appName is empty on Install")
	}
	if err := mgrEmptyApp.Update(ctx, "toolA"); err == nil {
		t.Errorf("expected error when appName is empty on Update")
	}

	// InstallUnsatisfied
	_, err := mgr.InstallUnsatisfied(ctx)
	if err == nil {
		t.Errorf("expected error from InstallUnsatisfied with failing/missing installers")
	}

	// UpdateAll
	_, err = mgr.UpdateAll(ctx)
	if err == nil {
		t.Errorf("expected error from UpdateAll with failing installers")
	}
}

func TestIsNotFound(t *testing.T) {
	missing := &exec.Error{Name: "tool", Err: exec.ErrNotFound}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"executable not in PATH", missing, true},
		{"wrapped by a runner", fmt.Errorf("tool: %w: stderr", missing), true},
		{"explicit path does not exist", &fs.PathError{Op: "fork/exec", Path: "/missing/tool", Err: fs.ErrNotExist}, true},
		{"installed tool reports a missing file", errors.New("exit status 1: config file not found"), false},
		{"generic failure", errors.New("generic error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotFound(tt.err); got != tt.want {
				t.Errorf("isNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestVerifyReportsMissingOnlyForAbsentExecutables(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMissing bool
	}{
		{"absent", &exec.Error{Name: "toolA", Err: exec.ErrNotFound}, true},
		{"version check failed", errors.New("exit status 2: settings not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			mgr := New(Config{AppName: "testapp", Dependencies: []Dependency{{Name: "toolA", MinVersion: "1.0.0"}}, Runner: func(context.Context, string, ...string) ([]byte, error) {
				return nil, tt.err
			}})
			reports, err := mgr.Verify(context.Background())
			if err == nil || reports[0].Satisfied {
				t.Fatalf("reports=%+v err=%v", reports, err)
			}
			if missing := strings.Contains(reports[0].Error, "missing in $PATH"); missing != tt.wantMissing {
				t.Errorf("report error %q, want missing=%v", reports[0].Error, tt.wantMissing)
			}
		})
	}
}

func TestDefaultRunner(t *testing.T) {
	ctx := context.Background()
	out, err := DefaultRunner(ctx, "echo", "hello")
	if err != nil {
		t.Fatalf("DefaultRunner echo failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("unexpected output: %q", string(out))
	}

	// Command with error output
	_, err = DefaultRunner(ctx, "sh", "-c", "echo 'Error: test failure' >&2; exit 1")
	if err == nil {
		t.Errorf("expected error from exit 1")
	}
	if !strings.Contains(err.Error(), "Error: test failure") {
		t.Errorf("expected sanitized error message, got: %v", err)
	}
}
