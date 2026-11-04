package godeps

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ytDlpName is the executable name yt-dlp is invoked by, and the name of its
// package directory in the managed bin directory.
const ytDlpName = "yt-dlp"

// ErrUnsupportedPlatform reports an operating system and architecture with no
// matching release build.
var ErrUnsupportedPlatform = errors.New("unsupported platform")

// ytDlpAsset is an unpacked (PyInstaller one-folder) yt-dlp release build.
type ytDlpAsset struct {
	// archive is the release asset file name.
	archive string
	// executable is the entry point at the archive root, next to _internal/.
	executable string
}

// ytDlpAssets lists the unpacked builds by GOOS/GOARCH. The macOS build is a
// universal binary. yt-dlp publishes other platforms only as one-file builds,
// which unpack themselves into the system temporary directory on every run.
var ytDlpAssets = map[string]ytDlpAsset{
	"darwin/amd64":  {archive: "yt-dlp_macos.zip", executable: "yt-dlp_macos"},
	"darwin/arm64":  {archive: "yt-dlp_macos.zip", executable: "yt-dlp_macos"},
	"linux/amd64":   {archive: "yt-dlp_linux.zip", executable: "yt-dlp_linux"},
	"linux/arm64":   {archive: "yt-dlp_linux_aarch64.zip", executable: "yt-dlp_linux_aarch64"},
	"windows/amd64": {archive: "yt-dlp_win.zip", executable: "yt-dlp.exe"},
	"windows/386":   {archive: "yt-dlp_win_x86.zip", executable: "yt-dlp_x86.exe"},
	"windows/arm64": {archive: "yt-dlp_win_arm64.zip", executable: "yt-dlp_arm64.exe"},
}

func ytDlpAssetFor(goos, goarch string) (ytDlpAsset, error) {
	asset, ok := ytDlpAssets[goos+"/"+goarch]
	if !ok {
		return ytDlpAsset{}, fmt.Errorf("no unpacked yt-dlp build for %s/%s: %w", goos, goarch, ErrUnsupportedPlatform)
	}
	return asset, nil
}

// YtDlp creates an Installer for yt-dlp's unpacked release build. Install and
// Update both download the latest build with InstallYtDlp; the unpacked builds
// do not support yt-dlp -U.
func YtDlp() Installer {
	return InstallerFunc(InstallYtDlp)
}

// InstallYtDlp installs the latest unpacked yt-dlp build for the running
// platform into targetDir; see InstallYtDlpForPlatform.
func InstallYtDlp(ctx context.Context, targetDir string) error {
	return InstallYtDlpForPlatform(ctx, runtime.GOOS, runtime.GOARCH, targetDir)
}

// InstallYtDlpForPlatform installs the latest unpacked yt-dlp build for goos
// and goarch into the package directory targetDir/yt-dlp, with its entry point
// at targetDir/yt-dlp/yt-dlp (yt-dlp.exe on Windows). Unlike the one-file build,
// the unpacked build runs from that directory and writes nothing to the system
// temporary directory.
//
// The build is downloaded and extracted next to the package directory, then
// moved into place, so a failed installation leaves a previous one untouched.
// It replaces a previous installation, including an earlier one-file
// executable at targetDir/yt-dlp or targetDir/yt-dlp.exe. Use
// Manager.InitManagedPath or InitManagedPath to put the package directory on
// PATH.
func InstallYtDlpForPlatform(ctx context.Context, goos, goarch, targetDir string) error {
	asset, err := ytDlpAssetFor(goos, goarch)
	if err != nil {
		return err
	}
	downloadURL := fmt.Sprintf("%s/yt-dlp/yt-dlp/releases/latest/download/%s", strings.TrimRight(githubBaseURL, "/"), asset.archive)

	removeStaleLeftovers(targetDir, "."+ytDlpName+"-")
	archive, err := os.CreateTemp(targetDir, "."+ytDlpName+"-*.zip")
	if err != nil {
		return fmt.Errorf("creating yt-dlp download file: %w", err)
	}
	defer os.Remove(archive.Name())
	err = downloadFile(ctx, downloadURL, archive)
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("downloading yt-dlp: %w", err)
	}

	staging, err := os.MkdirTemp(targetDir, "."+ytDlpName+"-*")
	if err != nil {
		return fmt.Errorf("creating yt-dlp staging directory: %w", err)
	}
	// Moving staging into place leaves nothing here to remove.
	defer os.RemoveAll(staging)
	// MkdirTemp creates the directory private; it becomes the package directory.
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("setting yt-dlp directory permissions: %w", err)
	}
	if err := extractZipTree(archive.Name(), staging); err != nil {
		return fmt.Errorf("extracting %s: %w", asset.archive, err)
	}

	entryName := ytDlpName
	if goos == "windows" {
		entryName += ".exe"
	}
	entry := filepath.Join(staging, entryName)
	if err := os.Rename(filepath.Join(staging, asset.executable), entry); err != nil {
		return fmt.Errorf("%s has no %s at its root: %w", asset.archive, asset.executable, err)
	}
	if err := os.Chmod(entry, 0o755); err != nil {
		return fmt.Errorf("making %s executable: %w", entry, err)
	}

	if err := replaceDir(staging, filepath.Join(targetDir, ytDlpName)); err != nil {
		return fmt.Errorf("installing yt-dlp: %w", err)
	}
	// The Windows one-file build sat beside the package directory, ahead of it
	// on PATH.
	if goos == "windows" {
		if err := os.Remove(filepath.Join(targetDir, entryName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing the one-file yt-dlp build: %w", err)
		}
	}
	return nil
}

// staleLeftoverAge is how old an installer's download or staging entry must be
// before a later installation removes it. It is far longer than any
// installation takes, so a concurrent installation keeps its own entries.
const staleLeftoverAge = time.Hour

// removeStaleLeftovers removes entries in dir whose names start with prefix and
// that were last modified more than staleLeftoverAge ago. They are downloads,
// staging trees, and set-aside previous installations left behind when an
// installation was killed before its own cleanup ran.
func removeStaleLeftovers(dir, prefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleLeftoverAge)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

// replaceDir moves the directory staged to dest, replacing any file or
// directory already there. It restores the previous dest if the move fails.
func replaceDir(staged, dest string) error {
	holder, err := os.MkdirTemp(filepath.Dir(staged), "."+filepath.Base(dest)+"-old-*")
	if err != nil {
		return fmt.Errorf("creating directory for the previous installation: %w", err)
	}
	defer os.RemoveAll(holder)

	previous := filepath.Join(holder, filepath.Base(dest))
	if err := os.Rename(dest, previous); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("moving aside the previous installation: %w", err)
		}
		previous = ""
	}
	if err := os.Rename(staged, dest); err != nil {
		if previous != "" {
			_ = os.Rename(previous, dest)
		}
		return fmt.Errorf("moving the new installation into place: %w", err)
	}
	return nil
}

// extractZipTree extracts every entry of the zip archive at archivePath into
// destDir, preserving file permissions and relative symbolic links. All writes
// go through an os.Root, so neither an entry name nor a chain of extracted
// links can place anything outside destDir; link targets must also stay inside
// the archive root.
func extractZipTree(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}
	defer zr.Close()

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("opening extraction directory: %w", err)
	}
	defer root.Close()

	for _, f := range zr.File {
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q is outside the archive root", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = root.MkdirAll(name, 0o755)
		case mode&fs.ModeSymlink != 0:
			err = extractZipSymlink(root, f, name)
		case mode.IsRegular():
			err = extractZipFile(root, f, name, mode.Perm())
		default:
			err = fmt.Errorf("unsupported file type %s", mode.Type())
		}
		if err != nil {
			return fmt.Errorf("extracting %q: %w", f.Name, err)
		}
	}
	return nil
}

func extractZipFile(root *os.Root, f *zip.File, name string, perm fs.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	// The owner always needs to read and replace the installed files.
	dst, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm|0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	return err
}

func extractZipSymlink(root *os.Root, f *zip.File, name string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	target, err := io.ReadAll(src)
	_ = src.Close()
	if err != nil {
		return err
	}
	link := filepath.FromSlash(string(target))
	if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), link)) {
		return fmt.Errorf("symbolic link target %q is outside the archive root", target)
	}
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return root.Symlink(link, name)
}
