package godeps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetManagedBinDir(t *testing.T) {
	// Empty app name error
	if _, err := GetManagedBinDir(""); err == nil {
		t.Errorf("expected error for empty appName")
	}

	// Custom XDG_DATA_HOME
	tempDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tempDir)
	binDir, err := GetManagedBinDir("testapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tempDir, "testapp", "bin")
	if binDir != expected {
		t.Errorf("GetManagedBinDir() = %q, want %q", binDir, expected)
	}

	// Fallback when XDG_DATA_HOME is unset
	t.Setenv("XDG_DATA_HOME", "")
	binDir, err = GetManagedBinDir("testapp")
	if err != nil {
		t.Fatalf("unexpected error on unset XDG_DATA_HOME: %v", err)
	}
	if !strings.Contains(binDir, "testapp") {
		t.Errorf("expected path containing testapp, got: %s", binDir)
	}
}

func TestEnsureManagedBinDir(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tempDir)

	binDir, err := EnsureManagedBinDir("testapp")
	if err != nil {
		t.Fatalf("EnsureManagedBinDir() error: %v", err)
	}

	if fi, err := os.Stat(binDir); err != nil || !fi.IsDir() {
		t.Errorf("expected directory %q to exist", binDir)
	}

	// Empty appName error
	if _, err := EnsureManagedBinDir(""); err == nil {
		t.Errorf("expected error for empty appName")
	}
}

func TestInitManagedPath(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tempDir)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", oldPath)

	err := InitManagedPath("testapp")
	if err != nil {
		t.Fatalf("InitManagedPath() error: %v", err)
	}

	binDir, _ := GetManagedBinDir("testapp")
	newPath := os.Getenv("PATH")
	if !strings.HasPrefix(newPath, binDir) {
		t.Errorf("PATH does not start with managed bin dir %q, got PATH=%q", binDir, newPath)
	}

	// Calling again should be idempotent
	if err := InitManagedPath("testapp"); err != nil {
		t.Errorf("second call to InitManagedPath failed: %v", err)
	}

	// Empty appName error
	if err := InitManagedPath(""); err == nil {
		t.Errorf("expected error for empty appName in InitManagedPath")
	}
}
