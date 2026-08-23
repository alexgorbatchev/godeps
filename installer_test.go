package godeps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestTarGz(t *testing.T, binName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: binName,
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("writing tar header: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("writing tar content: %v", err)
	}
	_ = tw.Close()
	_ = gzw.Close()
	return buf.Bytes()
}

func createTestZip(t *testing.T, binName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create(binName)
	if err != nil {
		t.Fatalf("creating zip entry: %v", err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("writing zip content: %v", err)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestResolveLatestTag(t *testing.T) {
	ctx := context.Background()

	// 1. HEAD redirect success
	tsHead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/owner/repo/releases/tag/v1.2.3", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer tsHead.Close()

	tag, err := ResolveLatestTagWithBaseURL(ctx, tsHead.URL, "owner", "repo")
	if err != nil || tag != "v1.2.3" {
		t.Fatalf("ResolveLatestTag HEAD failed: tag=%q, err=%v", tag, err)
	}

	// 2. GET redirect fallback
	tsGet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/owner/repo/releases/tag/v2.0.0", http.StatusFound)
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer tsGet.Close()

	tag, err = ResolveLatestTagWithBaseURL(ctx, tsGet.URL, "owner", "repo")
	if err != nil || tag != "v2.0.0" {
		t.Fatalf("ResolveLatestTag GET fallback failed: tag=%q, err=%v", tag, err)
	}

	// 3. Error 404
	ts404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts404.Close()

	if _, err := ResolveLatestTagWithBaseURL(ctx, ts404.URL, "owner", "repo"); err == nil {
		t.Errorf("expected error for 404 release")
	}

	// 4. Default wrapper test
	oldBase := githubBaseURL
	githubBaseURL = tsHead.URL
	defer func() { githubBaseURL = oldBase }()

	if _, err := ResolveLatestTag(ctx, "owner", "repo"); err != nil {
		t.Errorf("ResolveLatestTag failed: %v", err)
	}
}

func TestDownloadAndExtractAsset_TarGz(t *testing.T) {
	tempDir := t.TempDir()
	tarData := createTestTarGz(t, "mybin", "#!/bin/sh\necho ok\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(tarData)
	}))
	defer ts.Close()

	ctx := context.Background()
	err := downloadAndExtractAsset(ctx, ts.URL+"/mybin.tar.gz", "mybin", tempDir)
	if err != nil {
		t.Fatalf("downloadAndExtractAsset failed: %v", err)
	}

	destPath := filepath.Join(tempDir, "mybin")
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("extracted binary %q does not exist", destPath)
	}

	// Error: missing binary in tar
	emptyTar := createTestTarGz(t, "different_bin", "content")
	tsEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(emptyTar)
	}))
	defer tsEmpty.Close()

	if err := downloadAndExtractAsset(ctx, tsEmpty.URL+"/empty.tar.gz", "mybin", tempDir); err == nil {
		t.Errorf("expected error when binary missing in tar archive")
	}

	// Invalid gzip data
	if err := extractTarGzBinary([]byte("not gzip"), "mybin", destPath); err == nil {
		t.Errorf("expected error on invalid gzip data")
	}

	// Error opening dest file
	if err := extractTarGzBinary(tarData, "mybin", "/dev/null/invalid_file"); err == nil {
		t.Errorf("expected error extracting tar to invalid dest path")
	}
}

func TestDownloadAndExtractAsset_Zip(t *testing.T) {
	tempDir := t.TempDir()
	zipData := createTestZip(t, "mybin", "zip binary content")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	ctx := context.Background()
	err := downloadAndExtractAsset(ctx, ts.URL+"/mybin.zip", "mybin", tempDir)
	if err != nil {
		t.Fatalf("downloadAndExtractAsset failed: %v", err)
	}

	destPath := filepath.Join(tempDir, "mybin")
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("extracted binary %q does not exist", destPath)
	}

	// Error: missing binary in zip
	differentZip := createTestZip(t, "other_bin", "content")
	tsDiff := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(differentZip)
	}))
	defer tsDiff.Close()

	if err := downloadAndExtractAsset(ctx, tsDiff.URL+"/diff.zip", "mybin", tempDir); err == nil {
		t.Errorf("expected error when binary missing in zip archive")
	}

	// Invalid zip data
	if err := extractZipBinary([]byte("not zip"), "mybin", destPath); err == nil {
		t.Errorf("expected error on invalid zip data")
	}

	// Error opening dest file in zip extraction
	if err := extractZipBinary(zipData, "mybin", "/dev/null/invalid_file"); err == nil {
		t.Errorf("expected error extracting zip to invalid dest path")
	}
}

func TestDownloadAndExtractGoBinary_Fallback(t *testing.T) {
	tempDir := t.TempDir()
	tarData := createTestTarGz(t, "mybin", "#!/bin/sh\necho ok\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/owner/repo/releases/tag/v1.0.0", http.StatusFound)
		case strings.Contains(r.URL.Path, "repo_1.0.0"):
			http.NotFound(w, r)
		case strings.Contains(r.URL.Path, "mybin_1.0.0"):
			w.Header().Set("Content-Type", "application/gzip")
			w.Write(tarData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	defer func() { githubBaseURL = oldBase }()

	ctx := context.Background()
	if err := DownloadAndExtractGoBinary(ctx, "owner", "repo", "mybin", tempDir); err != nil {
		t.Fatalf("DownloadAndExtractGoBinary fallback failed: %v", err)
	}

	// Failure case where both fail
	tsFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/owner/repo/releases/tag/v1.0.0", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer tsFail.Close()
	githubBaseURL = tsFail.URL

	if err := DownloadAndExtractGoBinary(ctx, "owner", "repo", "mybin", tempDir); err == nil {
		t.Errorf("expected error when both download attempts fail")
	}
}

func TestDownloadAndExtractAsset_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. HTTP 404 error
	ts404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts404.Close()

	if err := downloadAndExtractAsset(ctx, ts404.URL+"/file", "bin", tempDir); err == nil {
		t.Errorf("expected error on HTTP 404")
	}

	// 2. Corrupted tar.gz
	tsCorruptTar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a valid gzip"))
	}))
	defer tsCorruptTar.Close()

	if err := downloadAndExtractAsset(ctx, tsCorruptTar.URL+"/bad.tar.gz", "bin", tempDir); err == nil {
		t.Errorf("expected error on corrupt tar.gz")
	}

	// 3. Corrupted zip
	tsCorruptZip := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a valid zip"))
	}))
	defer tsCorruptZip.Close()

	if err := downloadAndExtractAsset(ctx, tsCorruptZip.URL+"/bad.zip", "bin", tempDir); err == nil {
		t.Errorf("expected error on corrupt zip")
	}

	// 4. Raw file download
	tsRaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/sh\nraw binary\n"))
	}))
	defer tsRaw.Close()

	if err := downloadAndExtractAsset(ctx, tsRaw.URL+"/rawbin", "rawbin", tempDir); err != nil {
		t.Errorf("raw binary extraction failed: %v", err)
	}
}

func TestDownloadDirectBinary(t *testing.T) {
	tempDir := t.TempDir()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("direct executable content"))
	}))
	defer ts.Close()

	ctx := context.Background()
	err := DownloadDirectBinary(ctx, ts.URL+"/directbin", "directbin", tempDir)
	if err != nil {
		t.Fatalf("DownloadDirectBinary failed: %v", err)
	}

	destPath := filepath.Join(tempDir, "directbin")
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("direct binary %q does not exist", destPath)
	}

	// HTTP error
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer tsErr.Close()

	if err := DownloadDirectBinary(ctx, tsErr.URL+"/fail", "fail", tempDir); err == nil {
		t.Errorf("expected error on HTTP 500")
	}

	// Invalid URL error
	if err := DownloadDirectBinary(ctx, "http://invalid-nonexistent-domain-12345.xyz/bin", "bin", t.TempDir()); err == nil {
		t.Errorf("expected error for invalid domain URL")
	}

	// Target dir permission / creation error
	if err := DownloadDirectBinary(ctx, ts.URL, "bin", "/dev/null/invalid_dir"); err == nil {
		t.Errorf("expected error writing to invalid dir")
	}
}

func TestInstallYtDlpForPlatform(t *testing.T) {
	tempDir := t.TempDir()
	var requestedPaths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		w.Write([]byte("mock yt-dlp binary"))
	}))
	defer ts.Close()

	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	defer func() { githubBaseURL = oldBase }()

	ctx := context.Background()
	tests := []struct {
		goos     string
		goarch   string
		wantPath string
	}{
		{"darwin", "arm64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos"},
		{"darwin", "amd64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos"},
		{"windows", "amd64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe"},
		{"linux", "arm64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux_aarch64"},
		{"linux", "amd64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux"},
		{"freebsd", "amd64", "/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"},
	}

	for _, tt := range tests {
		requestedPaths = nil
		err := InstallYtDlpForPlatform(ctx, tt.goos, tt.goarch, tempDir)
		if err != nil {
			t.Fatalf("InstallYtDlpForPlatform(%s, %s) error: %v", tt.goos, tt.goarch, err)
		}
		if len(requestedPaths) == 0 || requestedPaths[len(requestedPaths)-1] != tt.wantPath {
			t.Errorf("InstallYtDlpForPlatform(%s, %s) requested %v, want %s", tt.goos, tt.goarch, requestedPaths, tt.wantPath)
		}
	}

	// Wrapper test
	if err := InstallYtDlp(ctx, tempDir); err != nil {
		t.Errorf("InstallYtDlp wrapper failed: %v", err)
	}
}

func TestUpdateYtDlp(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Success via runner
	okRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("yt-dlp is up to date"), nil
	}
	if err := UpdateYtDlp(ctx, okRunner, tempDir); err != nil {
		t.Errorf("UpdateYtDlp failed on success runner: %v", err)
	}

	// 2. Runner returns error -> fallback to download
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/sh\n"))
	}))
	defer ts.Close()

	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	defer func() { githubBaseURL = oldBase }()

	errRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("ERROR: update failed"), errors.New("exit 1")
	}
	_ = UpdateYtDlp(ctx, errRunner, tempDir)

	// 3. Default runner (nil runner arg)
	_ = UpdateYtDlp(ctx, nil, tempDir)
}

func TestInstallPackage_OSBranches(t *testing.T) {
	ctx := context.Background()
	mockRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("ok"), nil
	}

	oldLookPath := lookPathFunc
	defer func() { lookPathFunc = oldLookPath }()

	// Test pacman, dnf, apt, brew, winget, choco
	packageManagers := []string{"brew", "apt-get", "pacman", "dnf", "winget", "choco"}
	for _, pm := range packageManagers {
		lookPathFunc = func(file string) (string, error) {
			if file == pm {
				return "/usr/bin/" + pm, nil
			}
			return "", errors.New("not found")
		}

		osName := "linux"
		if pm == "brew" {
			osName = "darwin"
		} else if pm == "winget" || pm == "choco" {
			osName = "windows"
		}

		if err := installPackageForOS(ctx, osName, "ffmpeg", mockRunner); err != nil {
			t.Errorf("installPackageForOS with %s on %s failed: %v", pm, osName, err)
		}
	}

	// Wrapper test
	lookPathFunc = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	if err := InstallPackage(ctx, "ffmpeg", mockRunner); err != nil {
		t.Errorf("InstallPackage wrapper failed: %v", err)
	}
	_ = InstallPackage(ctx, "ffmpeg", nil)

	// Unsupported OS/package manager
	lookPathFunc = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	if err := installPackageForOS(ctx, "unknown_os", "ffmpeg", mockRunner); err == nil {
		t.Errorf("expected error for unsupported OS")
	}
}

func TestBuiltInInstallers(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/owner/repo/releases/tag/v1.0.0", http.StatusFound)
			return
		}
		w.Write(createTestTarGz(t, "mybin", "ok"))
	}))
	defer ts.Close()

	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	defer func() { githubBaseURL = oldBase }()

	ghInstaller := GitHubReleaseGoBinary("owner", "repo", "mybin")
	if err := ghInstaller.Install(ctx, tempDir); err != nil {
		t.Errorf("GitHubReleaseGoBinary.Install() error: %v", err)
	}
	if err := ghInstaller.Update(ctx, tempDir); err != nil {
		t.Errorf("GitHubReleaseGoBinary.Update() error: %v", err)
	}

	ytInstaller := YtDlp()
	if err := ytInstaller.Install(ctx, tempDir); err != nil {
		t.Errorf("YtDlp().Install() error: %v", err)
	}
	if err := ytInstaller.Update(ctx, tempDir); err != nil {
		t.Errorf("YtDlp().Update() error: %v", err)
	}

	pkgInstaller := SystemPackageManager("ffmpeg")
	lookPathFunc = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	_ = pkgInstaller.Install(ctx, tempDir)
	_ = pkgInstaller.Update(ctx, tempDir)

	fInstaller := InstallerFunc(func(ctx context.Context, targetDir string) error {
		return nil
	})
	if err := fInstaller.Install(ctx, tempDir); err != nil {
		t.Errorf("InstallerFunc.Install() error: %v", err)
	}
	if err := fInstaller.Update(ctx, tempDir); err != nil {
		t.Errorf("InstallerFunc.Update() error: %v", err)
	}
}
