package godeps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	githubBaseURL = "https://github.com"
	lookPathFunc  = exec.LookPath
)

// Installer defines how a dependency is installed and updated.
type Installer interface {
	Install(ctx context.Context, targetDir string) error
	Update(ctx context.Context, targetDir string) error
}

// InstallerFunc allows using plain functions as an Installer.
type InstallerFunc func(ctx context.Context, targetDir string) error

func (f InstallerFunc) Install(ctx context.Context, targetDir string) error {
	return f(ctx, targetDir)
}

func (f InstallerFunc) Update(ctx context.Context, targetDir string) error {
	return f(ctx, targetDir)
}

// GitHubReleaseGoBinary creates an Installer for standard Go binary releases on GitHub (tar.gz/zip).
func GitHubReleaseGoBinary(owner, repo, binName string) Installer {
	return &gitHubGoBinaryInstaller{
		owner:   owner,
		repo:    repo,
		binName: binName,
	}
}

type gitHubGoBinaryInstaller struct {
	owner   string
	repo    string
	binName string
}

func (g *gitHubGoBinaryInstaller) Install(ctx context.Context, targetDir string) error {
	return DownloadAndExtractGoBinary(ctx, g.owner, g.repo, g.binName, targetDir)
}

func (g *gitHubGoBinaryInstaller) Update(ctx context.Context, targetDir string) error {
	return DownloadAndExtractGoBinary(ctx, g.owner, g.repo, g.binName, targetDir)
}

// SystemPackageManager creates an Installer that uses the host system package manager (brew, apt, pacman, etc.).
func SystemPackageManager(packageName string) Installer {
	return &pkgManagerInstaller{pkgName: packageName}
}

type pkgManagerInstaller struct {
	pkgName string
}

func (p *pkgManagerInstaller) Install(ctx context.Context, targetDir string) error {
	return InstallPackage(ctx, p.pkgName, packageManagerRunner)
}

func (p *pkgManagerInstaller) Update(ctx context.Context, targetDir string) error {
	return InstallPackage(ctx, p.pkgName, packageManagerRunner)
}

// ResolveLatestTag queries the latest release tag for a GitHub repository without using the GitHub API rate limits.
func ResolveLatestTag(ctx context.Context, owner, repo string) (string, error) {
	return ResolveLatestTagWithBaseURL(ctx, githubBaseURL, owner, repo)
}

// ResolveLatestTagWithBaseURL queries the latest release tag for a repository from a custom base URL.
func ResolveLatestTagWithBaseURL(ctx context.Context, baseURL, owner, repo string) (string, error) {
	targetURL := fmt.Sprintf("%s/%s/%s/releases/latest", strings.TrimRight(baseURL, "/"), owner, repo)

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating HEAD request to %s: %w", targetURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying latest release redirect from %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusSeeOther {
		loc := resp.Header.Get("Location")
		if loc != "" {
			parts := strings.Split(loc, "/")
			if len(parts) > 0 {
				tag := parts[len(parts)-1]
				if tag != "" {
					return tag, nil
				}
			}
		}
	}

	// Fallback to GET
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating GET request to %s: %w", targetURL, err)
	}
	getResp, err := client.Do(getReq)
	if err != nil {
		return "", fmt.Errorf("querying latest release GET redirect from %s: %w", targetURL, err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode == http.StatusFound || getResp.StatusCode == http.StatusMovedPermanently || getResp.StatusCode == http.StatusSeeOther {
		loc := getResp.Header.Get("Location")
		if loc != "" {
			parts := strings.Split(loc, "/")
			if len(parts) > 0 {
				tag := parts[len(parts)-1]
				if tag != "" {
					return tag, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not resolve latest release tag for %s/%s (HTTP status %d)", owner, repo, resp.StatusCode)
}

// DownloadAndExtractGoBinary downloads a release asset from GitHub, extracts it, and saves the binary.
func DownloadAndExtractGoBinary(ctx context.Context, owner, repo, binName, targetDir string) error {
	tag, err := ResolveLatestTag(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("resolving latest release for %s/%s: %w", owner, repo, err)
	}

	versionStr := strings.TrimPrefix(tag, "v")
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	archiveExt := "tar.gz"
	if goos == "windows" {
		archiveExt = "zip"
	}

	assetName := fmt.Sprintf("%s_%s_%s_%s.%s", repo, versionStr, goos, goarch, archiveExt)
	downloadURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", strings.TrimRight(githubBaseURL, "/"), owner, repo, tag, assetName)

	err = downloadAndExtractAsset(ctx, downloadURL, binName, targetDir)
	if err != nil {
		// Fallback without repo name prefix (e.g. name_version_os_arch)
		fallbackAsset := fmt.Sprintf("%s_%s_%s_%s.%s", binName, versionStr, goos, goarch, archiveExt)
		fallbackURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", strings.TrimRight(githubBaseURL, "/"), owner, repo, tag, fallbackAsset)
		if fallbackErr := downloadAndExtractAsset(ctx, fallbackURL, binName, targetDir); fallbackErr == nil {
			return nil
		}
		return fmt.Errorf("installing %s from %s: %w", binName, downloadURL, err)
	}

	return nil
}

func downloadAndExtractAsset(ctx context.Context, downloadURL, binName, targetDir string) error {
	var body bytes.Buffer
	if err := downloadFile(ctx, downloadURL, &body); err != nil {
		return err
	}
	bodyBytes := body.Bytes()

	targetExt := ""
	if runtime.GOOS == "windows" {
		targetExt = ".exe"
	}
	finalBinName := binName + targetExt
	destPath := filepath.Join(targetDir, finalBinName)
	tmpPath := destPath + ".tmp"
	_ = os.Remove(tmpPath)

	if strings.HasSuffix(downloadURL, ".tar.gz") || strings.HasSuffix(downloadURL, ".tgz") {
		if err := extractTarGzBinary(bodyBytes, binName, tmpPath); err != nil {
			return err
		}
	} else if strings.HasSuffix(downloadURL, ".zip") {
		if err := extractZipBinary(bodyBytes, binName, tmpPath); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(tmpPath, bodyBytes, 0755); err != nil {
			return fmt.Errorf("writing raw binary to %q: %w", tmpPath, err)
		}
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("setting executable permissions: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("moving binary into place at %q: %w", destPath, err)
	}

	return nil
}

func extractTarGzBinary(data []byte, binName, destPath string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	targetExt := ""
	if runtime.GOOS == "windows" {
		targetExt = ".exe"
	}
	expectedName := binName + targetExt

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar archive: %w", err)
		}

		baseName := filepath.Base(header.Name)
		if baseName == expectedName || baseName == binName {
			outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("creating output binary %q: %w", destPath, err)
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				_ = outFile.Close()
				_ = os.Remove(destPath)
				return fmt.Errorf("extracting binary from tar: %w", err)
			}
			_ = outFile.Close()
			return nil
		}
	}

	return fmt.Errorf("binary %q not found in tar archive", binName)
}

func extractZipBinary(data []byte, binName, destPath string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("creating zip reader: %w", err)
	}

	targetExt := ""
	if runtime.GOOS == "windows" {
		targetExt = ".exe"
	}
	expectedName := binName + targetExt

	for _, f := range zr.File {
		baseName := filepath.Base(f.Name)
		if baseName == expectedName || baseName == binName {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("opening zip entry %q: %w", f.Name, err)
			}

			outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				_ = rc.Close()
				return fmt.Errorf("creating output binary %q: %w", destPath, err)
			}

			if _, err := io.Copy(outFile, rc); err != nil {
				_ = rc.Close()
				_ = outFile.Close()
				_ = os.Remove(destPath)
				return fmt.Errorf("extracting binary from zip: %w", err)
			}
			_ = rc.Close()
			_ = outFile.Close()
			return nil
		}
	}

	return fmt.Errorf("binary %q not found in zip archive", binName)
}

// DownloadDirectBinary downloads a single executable file and writes it to targetDir/binName.
func DownloadDirectBinary(ctx context.Context, downloadURL, binName, targetDir string) error {
	destPath := filepath.Join(targetDir, binName)
	tmpPath := destPath + ".tmp"
	_ = os.Remove(tmpPath)

	outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("creating temporary binary %q: %w", tmpPath, err)
	}
	err = downloadFile(ctx, downloadURL, outFile)
	if closeErr := outFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("setting executable permissions: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("moving binary to %q: %w", destPath, err)
	}

	return nil
}

// downloadFile writes the body of a successful GET of downloadURL to out.
func downloadFile(ctx context.Context, downloadURL string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("creating download request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d from %s", resp.StatusCode, downloadURL)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("writing download from %s: %w", downloadURL, err)
	}
	return nil
}

// InstallPackage attempts to install a system package using host package managers (brew, apt, pacman, etc.).
// A nil runner runs the package manager as SystemPackageManager does: in the
// caller's process group, so it can prompt through the terminal.
func InstallPackage(ctx context.Context, packageName string, runner CommandRunner) error {
	return installPackageForOS(ctx, runtime.GOOS, packageName, runner)
}

func installPackageForOS(ctx context.Context, goos, packageName string, runner CommandRunner) error {
	if runner == nil {
		runner = packageManagerRunner
	}

	switch goos {
	case "darwin":
		if _, err := lookPathFunc("brew"); err == nil {
			_, err := runner(ctx, "brew", "install", packageName)
			return err
		}
	case "linux":
		if _, err := lookPathFunc("apt-get"); err == nil {
			_, err := runner(ctx, "apt-get", "install", "-y", packageName)
			return err
		}
		if _, err := lookPathFunc("pacman"); err == nil {
			_, err := runner(ctx, "pacman", "-S", "--noconfirm", packageName)
			return err
		}
		if _, err := lookPathFunc("dnf"); err == nil {
			_, err := runner(ctx, "dnf", "install", "-y", packageName)
			return err
		}
	case "windows":
		if _, err := lookPathFunc("winget"); err == nil {
			_, err := runner(ctx, "winget", "install", packageName, "-e", "--silent")
			return err
		}
		if _, err := lookPathFunc("choco"); err == nil {
			_, err := runner(ctx, "choco", "install", packageName, "-y")
			return err
		}
	}

	return fmt.Errorf("could not auto-install %s: no supported package manager found", packageName)
}
