package godeps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpgradeSelf checks for a newer GitHub release of the running application and replaces the executable in-place.
func UpgradeSelf(ctx context.Context, owner, repo, currentVersion string) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving running executable path: %w", err)
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("resolving executable symlinks: %w", err)
	}

	return UpgradeSelfToPath(ctx, owner, repo, currentVersion, execPath)
}

// UpgradeSelfToPath downloads the latest release binary for owner/repo and replaces the binary at destPath.
func UpgradeSelfToPath(ctx context.Context, owner, repo, currentVersion, destPath string) (string, error) {
	latestTag, err := ResolveLatestTag(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("resolving latest release for %s/%s: %w", owner, repo, err)
	}

	latestVersion := strings.TrimPrefix(latestTag, "v")
	cleanCurrent := strings.TrimPrefix(currentVersion, "v")

	if cleanCurrent != "dev" && cleanCurrent != "" {
		if compErr := CompareVersions(cleanCurrent, latestVersion); compErr == nil {
			return "", fmt.Errorf("already at the latest version (%s)", currentVersion)
		}
	}

	targetDir := filepath.Dir(destPath)
	binName := filepath.Base(destPath)
	binName = strings.TrimSuffix(binName, ".exe")

	if err := DownloadAndExtractGoBinary(ctx, owner, repo, binName, targetDir); err != nil {
		return "", fmt.Errorf("downloading latest %s/%s release %s: %w", owner, repo, latestTag, err)
	}

	return latestVersion, nil
}
