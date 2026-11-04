//go:build unix

package godeps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTool writes an executable script at dir/name that prints version.
func writeTool(t *testing.T, dir, name, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// managedFixture isolates the managed directory and PATH for one test. Its
// PATH holds a directory for copies installed outside the managed directory,
// followed by the system directories the scripts need.
type managedFixture struct {
	binDir    string
	globalDir string
	system    string
}

func newManagedFixture(t *testing.T) managedFixture {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	binDir, err := GetManagedBinDir("testapp")
	if err != nil {
		t.Fatal(err)
	}
	f := managedFixture{binDir: binDir, globalDir: t.TempDir(), system: "/usr/bin" + string(os.PathListSeparator) + "/bin"}
	t.Setenv("PATH", f.globalDir+string(os.PathListSeparator)+f.system)
	return f
}

func resolve(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func TestInitManagedPathAppendsManagedDirectories(t *testing.T) {
	f := newManagedFixture(t)
	writeTool(t, filepath.Join(f.binDir, "yt-dlp"), "yt-dlp", "2026.10.26")
	if err := os.MkdirAll(filepath.Join(f.binDir, ".yt-dlp-staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, f.globalDir, "tool", "1.0.0")
	writeTool(t, f.binDir, "tool", "2.0.0")

	if err := InitManagedPath("testapp"); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{f.globalDir, "/usr/bin", "/bin", f.binDir, filepath.Join(f.binDir, "yt-dlp")}, string(os.PathListSeparator))
	if got := os.Getenv("PATH"); got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got := resolve(t, "tool"); got != filepath.Join(f.globalDir, "tool") {
		t.Errorf("tool resolves to %q, want the copy outside the managed directory", got)
	}
	if got := resolve(t, "yt-dlp"); got != filepath.Join(f.binDir, "yt-dlp", "yt-dlp") {
		t.Errorf("yt-dlp resolves to %q, want the managed package", got)
	}

	// Directories already on PATH keep their position.
	t.Setenv("PATH", f.binDir+string(os.PathListSeparator)+f.system)
	if err := InitManagedPath("testapp"); err != nil {
		t.Fatal(err)
	}
	want = strings.Join([]string{f.binDir, "/usr/bin", "/bin", filepath.Join(f.binDir, "yt-dlp")}, string(os.PathListSeparator))
	if got := os.Getenv("PATH"); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}

func TestManagerInitManagedPathPrefersSatisfyingCopies(t *testing.T) {
	tests := []struct {
		name string
		// global and managed are the versions of the copies outside and inside
		// the managed directory; empty means no copy.
		global, managed string
		// pkg installs the managed copy as a package directory.
		pkg           bool
		wantGlobal    bool
		wantSatisfied bool
	}{
		{name: "satisfying global copy wins over a managed package", global: "2026.10.26", managed: "2026.10.26", pkg: true, wantGlobal: true, wantSatisfied: true},
		{name: "managed package replaces an outdated global copy", global: "2023.01.01", managed: "2026.10.26", pkg: true, wantSatisfied: true},
		{name: "managed package fills in a missing global copy", managed: "2026.10.26", pkg: true, wantSatisfied: true},
		{name: "outdated managed package does not replace an outdated global copy", global: "2023.01.01", managed: "2023.06.01", pkg: true, wantGlobal: true},
		{name: "satisfying global copy wins over a managed executable", global: "2026.10.26", managed: "2026.10.26", wantGlobal: true, wantSatisfied: true},
		{name: "managed executable replaces an outdated global copy", global: "2023.01.01", managed: "2026.10.26", wantSatisfied: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagedFixture(t)
			managedDir := f.binDir
			if tt.pkg {
				managedDir = filepath.Join(f.binDir, "yt-dlp")
			}
			if tt.managed != "" {
				writeTool(t, managedDir, "yt-dlp", tt.managed)
			}
			if tt.global != "" {
				writeTool(t, f.globalDir, "yt-dlp", tt.global)
			}
			// A second dependency whose global copy satisfies it, with a managed
			// copy in the bin directory.
			writeTool(t, f.globalDir, "helper", "2.0.0")
			writeTool(t, f.binDir, "helper", "2.0.0")
			m := New(Config{AppName: "testapp", Dependencies: []Dependency{
				{Name: "yt-dlp", MinVersion: "2024.08.01"},
				{Name: "helper", MinVersion: "1.0.0"},
			}})

			if err := m.InitManagedPath(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(managedDir, "yt-dlp")
			if tt.wantGlobal {
				want = filepath.Join(f.globalDir, "yt-dlp")
			}
			if got := resolve(t, "yt-dlp"); got != want {
				t.Errorf("yt-dlp resolves to %q, want %q (PATH=%q)", got, want, os.Getenv("PATH"))
			}
			// Moving a package directory ahead never shadows another dependency.
			if tt.pkg {
				if got := resolve(t, "helper"); got != filepath.Join(f.globalDir, "helper") {
					t.Errorf("helper resolves to %q, want the global copy", got)
				}
			}
			reports, err := m.Verify(context.Background())
			if (err == nil) != tt.wantSatisfied {
				t.Errorf("Verify() = %+v, %v; want satisfied=%v", reports, err, tt.wantSatisfied)
			}
		})
	}
}

// A later call demotes a managed directory that an earlier call moved ahead
// once the global copy satisfies the dependency again.
func TestManagerInitManagedPathRestoresGlobalPrecedence(t *testing.T) {
	f := newManagedFixture(t)
	pkgDir := filepath.Join(f.binDir, "yt-dlp")
	writeTool(t, pkgDir, "yt-dlp", "2026.10.26")
	global := writeTool(t, f.globalDir, "yt-dlp", "2023.01.01")
	m := New(Config{AppName: "testapp", Dependencies: []Dependency{{Name: "yt-dlp", MinVersion: "2024.08.01"}}})

	if err := m.InitManagedPath(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), pkgDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want the package directory first", os.Getenv("PATH"))
	}
	writeTool(t, f.globalDir, "yt-dlp", "2026.11.01")
	if err := m.InitManagedPath(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, "yt-dlp"); got != global {
		t.Errorf("yt-dlp resolves to %q, want the updated global copy %q", got, global)
	}
}

func TestManagerInitManagedPathHonorsCancellation(t *testing.T) {
	f := newManagedFixture(t)
	writeTool(t, filepath.Join(f.binDir, "yt-dlp"), "yt-dlp", "2026.10.26")
	writeTool(t, f.globalDir, "yt-dlp", "2023.01.01")
	before := os.Getenv("PATH")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New(Config{AppName: "testapp", Dependencies: []Dependency{{Name: "yt-dlp", MinVersion: "2024.08.01"}}})
	if err := m.InitManagedPath(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := os.Getenv("PATH"); got != before {
		t.Errorf("PATH changed to %q", got)
	}
}

// Ensure updates an outdated global dependency into the managed directory and
// then uses the managed copy.
func TestEnsureUsesInstalledPackageOverOutdatedGlobalCopy(t *testing.T) {
	f := newManagedFixture(t)
	writeTool(t, f.globalDir, "yt-dlp", "2023.01.01")
	var calls []string
	installer := InstallerFunc(func(_ context.Context, targetDir string) error {
		calls = append(calls, targetDir)
		writeTool(t, filepath.Join(targetDir, "yt-dlp"), "yt-dlp", "2026.10.26")
		return nil
	})
	m := New(Config{AppName: "testapp", Dependencies: []Dependency{{Name: "yt-dlp", MinVersion: "2024.08.01", Installer: installer}}})

	var out strings.Builder
	if err := m.Ensure(context.Background(), EnsureOptions{AutoInstall: true, Output: &out}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != f.binDir {
		t.Errorf("installer calls %v, want one into %s", calls, f.binDir)
	}
	if got := resolve(t, "yt-dlp"); got != filepath.Join(f.binDir, "yt-dlp", "yt-dlp") {
		t.Errorf("yt-dlp resolves to %q, want the managed package", got)
	}
}

func TestManagedPathErrors(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := InitManagedPath(""); err == nil {
		t.Error("InitManagedPath with an empty app name succeeded")
	}
	if err := New(Config{}).InitManagedPath(context.Background()); err != nil {
		t.Errorf("Manager.InitManagedPath with the default app name: %v", err)
	}
	if err := (&Manager{}).InitManagedPath(context.Background()); err == nil {
		t.Error("Manager.InitManagedPath with an empty app name succeeded")
	}
	if _, err := managedPathDirs(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("managedPathDirs of a missing directory succeeded")
	}
	if got := findExecutable([]string{"relative", ""}, "sh"); got != "" {
		t.Errorf("findExecutable searched relative directories and found %q", got)
	}
}
