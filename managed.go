package godeps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// GetManagedBinDir returns the path to the application's isolated binary directory following XDG standards.
func GetManagedBinDir(appName string) (string, error) {
	if strings.TrimSpace(appName) == "" {
		return "", fmt.Errorf("appName cannot be empty")
	}

	if xdgData := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdgData != "" {
		return filepath.Join(xdgData, appName, "bin"), nil
	}

	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, appName, "bin"), nil
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".local", "share", appName, "bin"), nil
}

// EnsureManagedBinDir creates the application's managed bin directory if not present and returns its path.
func EnsureManagedBinDir(appName string) (string, error) {
	binDir, err := GetManagedBinDir(appName)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("creating managed directory %q: %w", binDir, err)
	}

	return binDir, nil
}

// InitManagedPath appends the managed directories to the end of the current
// process PATH, so copies installed elsewhere on PATH take precedence and
// managed copies fill in only for executables PATH lacks. The managed
// directories are the bin directory and each package directory inside it (see
// Manager.InitManagedPath). Directories already on PATH keep their position.
//
// Without dependency versions, it cannot tell that a copy elsewhere on PATH is
// too old; Manager.InitManagedPath can.
func InitManagedPath(appName string) error {
	binDir, err := EnsureManagedBinDir(appName)
	if err != nil {
		return err
	}
	managed, err := managedPathDirs(binDir)
	if err != nil {
		return err
	}

	entries := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range managed {
		if !containsDir(entries, dir) {
			entries = append(entries, dir)
		}
	}
	return setPath(entries)
}

// InitManagedPath places the managed directories on the current process PATH
// so that each dependency resolves to the copy it should use:
//
//   - A copy elsewhere on PATH that meets the minimum version is used, even when
//     a managed copy exists.
//   - The managed copy is used when PATH has no other copy, or when the other
//     copy is older than the minimum and the managed copy is not. In that case
//     the dependency's managed directory moves ahead of the rest of PATH.
//
// A dependency installed as a single executable lives in the managed bin
// directory; one installed as a directory tree, such as yt-dlp, lives in a
// package directory bin/<name>/ whose entry point is bin/<name>/<name>. Every
// package directory is its own PATH entry, so moving one ahead affects only its
// dependency. Single executables share the bin directory: moving it ahead
// makes every managed single executable take precedence.
//
// Version checks run only for dependencies that have both a managed copy and
// another copy on PATH, and they call the Manager's runner with absolute paths.
// It returns ctx.Err() without changing PATH once ctx is done.
func (m *Manager) InitManagedPath(ctx context.Context) error {
	binDir, err := EnsureManagedBinDir(m.appName)
	if err != nil {
		return err
	}
	managed, err := managedPathDirs(binDir)
	if err != nil {
		return err
	}

	var others []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !containsDir(managed, dir) {
			others = append(others, dir)
		}
	}

	var ahead []string
	for _, dep := range m.dependencies {
		dir := dependencyDir(binDir, dep.Name)
		managedCopy := findExecutable([]string{dir}, dep.Name)
		if managedCopy == "" {
			continue
		}
		otherCopy := findExecutable(others, dep.Name)
		if otherCopy == "" || m.satisfiedBy(ctx, dep, otherCopy) {
			continue
		}
		if m.satisfiedBy(ctx, dep, managedCopy) && !containsDir(ahead, dir) {
			ahead = append(ahead, dir)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entries := slices.Concat(ahead, others)
	for _, dir := range managed {
		if !containsDir(ahead, dir) {
			entries = append(entries, dir)
		}
	}
	return setPath(entries)
}

// managedPathDirs returns the managed bin directory followed by its package
// directories: subdirectories whose names do not start with a dot, which
// installers use for staging.
func managedPathDirs(binDir string) ([]string, error) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil, fmt.Errorf("listing managed directory %q: %w", binDir, err)
	}
	dirs := []string{binDir}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			dirs = append(dirs, filepath.Join(binDir, entry.Name()))
		}
	}
	return dirs, nil
}

// dependencyDir returns the managed directory holding name's entry point: its
// package directory when one exists, otherwise the bin directory.
func dependencyDir(binDir, name string) string {
	pkgDir := filepath.Join(binDir, name)
	if info, err := os.Stat(pkgDir); err == nil && info.IsDir() {
		return pkgDir
	}
	return binDir
}

// findExecutable returns the first executable named name in dirs, searching
// them as exec.LookPath searches PATH. Relative directories are skipped, as
// exec.LookPath refuses executables it would find through them.
func findExecutable(dirs []string, name string) string {
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path
		}
	}
	return ""
}

func containsDir(dirs []string, dir string) bool {
	clean := filepath.Clean(dir)
	return slices.ContainsFunc(dirs, func(d string) bool { return filepath.Clean(d) == clean })
}

func setPath(entries []string) error {
	return os.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator)))
}
