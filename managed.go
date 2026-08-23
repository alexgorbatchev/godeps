package godeps

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// InitManagedPath ensures the managed bin directory is prepended to the current process PATH.
func InitManagedPath(appName string) error {
	binDir, err := EnsureManagedBinDir(appName)
	if err != nil {
		return err
	}

	currentPath := os.Getenv("PATH")
	pathList := filepath.SplitList(currentPath)
	for _, p := range pathList {
		if filepath.Clean(p) == filepath.Clean(binDir) {
			return nil
		}
	}

	newPath := binDir + string(os.PathListSeparator) + currentPath
	return os.Setenv("PATH", newPath)
}
