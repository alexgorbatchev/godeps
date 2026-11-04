package godeps

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type zipEntry struct {
	name string
	mode fs.FileMode
	body string
}

func buildZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		hdr.SetMode(e.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("creating zip entry %q: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("writing zip entry %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// unpackedBuild mimics the layout of yt-dlp's one-folder release archives: the
// entry point at the root next to _internal/.
func unpackedBuild(t *testing.T, executable string) []byte {
	t.Helper()
	return buildZip(t,
		zipEntry{name: "_internal/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "_internal/base_library.zip", mode: 0o644, body: "library"},
		zipEntry{name: "_internal/current", mode: fs.ModeSymlink | 0o777, body: "base_library.zip"},
		zipEntry{name: executable, mode: 0o755, body: "#!/bin/sh\necho 2026.10.26\n"},
	)
}

// serveReleases points the GitHub base URL at a server that returns archives
// by release asset name and records requested paths.
func serveReleases(t *testing.T, archives map[string][]byte) func() []string {
	t.Helper()
	var mu sync.Mutex
	var requested []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path)
		mu.Unlock()
		body, ok := archives[strings.TrimPrefix(r.URL.Path, "/yt-dlp/yt-dlp/releases/latest/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	t.Cleanup(func() { githubBaseURL = oldBase })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requested)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestYtDlpAssetFor(t *testing.T) {
	tests := []struct {
		goos, goarch      string
		archive, execName string
	}{
		{"darwin", "arm64", "yt-dlp_macos.zip", "yt-dlp_macos"},
		{"darwin", "amd64", "yt-dlp_macos.zip", "yt-dlp_macos"},
		{"linux", "amd64", "yt-dlp_linux.zip", "yt-dlp_linux"},
		{"linux", "arm64", "yt-dlp_linux_aarch64.zip", "yt-dlp_linux_aarch64"},
		{"windows", "amd64", "yt-dlp_win.zip", "yt-dlp.exe"},
		{"windows", "386", "yt-dlp_win_x86.zip", "yt-dlp_x86.exe"},
		{"windows", "arm64", "yt-dlp_win_arm64.zip", "yt-dlp_arm64.exe"},
		{"linux", "386", "", ""},
		{"freebsd", "amd64", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			asset, err := ytDlpAssetFor(tt.goos, tt.goarch)
			if tt.archive == "" {
				if !errors.Is(err, ErrUnsupportedPlatform) {
					t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
				}
				return
			}
			if err != nil || asset.archive != tt.archive || asset.executable != tt.execName {
				t.Fatalf("got %+v, %v; want %s containing %s", asset, err, tt.archive, tt.execName)
			}
		})
	}
}

func TestInstallYtDlpForPlatformExtractsUnpackedBuild(t *testing.T) {
	platforms := []struct{ goos, goarch, entry string }{
		{"darwin", "arm64", "yt-dlp"},
		{"linux", "amd64", "yt-dlp"},
		{"linux", "arm64", "yt-dlp"},
		{"windows", "amd64", "yt-dlp.exe"},
		{"windows", "386", "yt-dlp.exe"},
		{"windows", "arm64", "yt-dlp.exe"},
	}
	archives := map[string][]byte{}
	for _, p := range platforms {
		asset, err := ytDlpAssetFor(p.goos, p.goarch)
		if err != nil {
			t.Fatal(err)
		}
		archives[asset.archive] = unpackedBuild(t, asset.executable)
	}
	requested := serveReleases(t, archives)

	for _, p := range platforms {
		t.Run(p.goos+"/"+p.goarch, func(t *testing.T) {
			targetDir := t.TempDir()
			before := len(requested())
			if err := InstallYtDlpForPlatform(context.Background(), p.goos, p.goarch, targetDir); err != nil {
				t.Fatal(err)
			}
			asset, _ := ytDlpAssetFor(p.goos, p.goarch)
			if got := requested()[before:]; !slices.Equal(got, []string{"/yt-dlp/yt-dlp/releases/latest/download/" + asset.archive}) {
				t.Errorf("requested %v, want %s", got, asset.archive)
			}
			// Only the package directory remains: no download or staging leftovers.
			if names := dirNames(t, targetDir); !slices.Equal(names, []string{"yt-dlp"}) {
				t.Errorf("target directory holds %v, want only yt-dlp", names)
			}
			pkgDir := filepath.Join(targetDir, "yt-dlp")
			if names := dirNames(t, pkgDir); !slices.Equal(names, []string{"_internal", p.entry}) {
				t.Errorf("package directory holds %v, want _internal and %s", names, p.entry)
			}
			// Windows has no permission bits to check.
			if runtime.GOOS != "windows" {
				if info, err := os.Stat(pkgDir); err != nil || info.Mode().Perm() != 0o755 {
					t.Errorf("package directory %v, %v; want mode 0755", info, err)
				}
				if info, err := os.Stat(filepath.Join(pkgDir, p.entry)); err != nil || info.Mode().Perm()&0o111 == 0 {
					t.Errorf("entry point %v, %v; want an executable file", info, err)
				}
			}
			if link, err := os.Readlink(filepath.Join(pkgDir, "_internal", "current")); err != nil || link != "base_library.zip" {
				t.Errorf("symbolic link = %q, %v; want base_library.zip", link, err)
			}
			if data, err := os.ReadFile(filepath.Join(pkgDir, "_internal", "base_library.zip")); err != nil || string(data) != "library" {
				t.Errorf("_internal/base_library.zip = %q, %v", data, err)
			}
		})
	}
}

func TestInstallYtDlpReplacesPreviousInstall(t *testing.T) {
	serveReleases(t, map[string][]byte{
		"yt-dlp_macos.zip": unpackedBuild(t, "yt-dlp_macos"),
		"yt-dlp_win.zip":   unpackedBuild(t, "yt-dlp.exe"),
	})
	tests := []struct {
		name       string
		goos       string
		previous   map[string]string
		entry      string
		wantAbsent string
	}{
		{name: "one-file build", goos: "darwin", previous: map[string]string{"yt-dlp": "one-file"}, entry: "yt-dlp"},
		{name: "Windows one-file build", goos: "windows", previous: map[string]string{"yt-dlp.exe": "one-file"}, entry: "yt-dlp.exe", wantAbsent: "yt-dlp.exe"},
		{name: "previous unpacked build", goos: "darwin", previous: map[string]string{"yt-dlp/yt-dlp": "old", "yt-dlp/_internal/stale.so": "stale"}, entry: "yt-dlp", wantAbsent: "yt-dlp/_internal/stale.so"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetDir := t.TempDir()
			for name, body := range tt.previous {
				path := filepath.Join(targetDir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := InstallYtDlpForPlatform(context.Background(), tt.goos, "amd64", targetDir); err != nil {
				t.Fatal(err)
			}
			entry, err := os.ReadFile(filepath.Join(targetDir, "yt-dlp", tt.entry))
			if err != nil || !strings.Contains(string(entry), "2026.10.26") {
				t.Errorf("entry point = %q, %v; want the new build", entry, err)
			}
			if tt.wantAbsent != "" {
				if _, err := os.Lstat(filepath.Join(targetDir, filepath.FromSlash(tt.wantAbsent))); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s still exists: %v", tt.wantAbsent, err)
				}
			}
			if names := dirNames(t, targetDir); !slices.Equal(names, []string{"yt-dlp"}) {
				t.Errorf("target directory holds %v, want only yt-dlp", names)
			}
		})
	}
}

func TestInstallYtDlpFailureKeepsPreviousInstall(t *testing.T) {
	tests := []struct {
		name    string
		archive []byte
	}{
		{name: "download fails"},
		{name: "corrupt archive", archive: []byte("not a zip")},
		{name: "archive without the executable", archive: buildZip(t, zipEntry{name: "_internal/lib", mode: 0o644})},
		{name: "entry outside the archive root", archive: buildZip(t, zipEntry{name: "../escaped", mode: 0o644}, zipEntry{name: "yt-dlp_macos", mode: 0o755})},
		{name: "link outside the archive root", archive: buildZip(t, zipEntry{name: "_internal/escaped", mode: fs.ModeSymlink | 0o777, body: "../../escaped"}, zipEntry{name: "yt-dlp_macos", mode: 0o755})},
		{name: "write through a chain of links", archive: buildZip(t,
			zipEntry{name: "a/b/l1", mode: fs.ModeSymlink | 0o777, body: ".."},
			zipEntry{name: "a/b/l2", mode: fs.ModeSymlink | 0o777, body: "l1/../.."},
			zipEntry{name: "a/b/l2/escaped", mode: 0o644},
			zipEntry{name: "yt-dlp_macos", mode: 0o755})},
		{name: "absolute link", archive: buildZip(t, zipEntry{name: "escaped", mode: fs.ModeSymlink | 0o777, body: "/etc"}, zipEntry{name: "yt-dlp_macos", mode: 0o755})},
		{name: "unsupported entry type", archive: buildZip(t, zipEntry{name: "pipe", mode: fs.ModeNamedPipe | 0o644}, zipEntry{name: "yt-dlp_macos", mode: 0o755})},
		{name: "duplicate entries", archive: buildZip(t, zipEntry{name: "yt-dlp_macos", mode: 0o755}, zipEntry{name: "yt-dlp_macos", mode: 0o755})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archives := map[string][]byte{}
			if tt.archive != nil {
				archives["yt-dlp_macos.zip"] = tt.archive
			}
			serveReleases(t, archives)
			root := t.TempDir()
			targetDir := filepath.Join(root, "bin")
			previous := filepath.Join(targetDir, "yt-dlp", "yt-dlp")
			if err := os.MkdirAll(filepath.Dir(previous), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(previous, []byte("previous"), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := InstallYtDlpForPlatform(context.Background(), "darwin", "arm64", targetDir); err == nil {
				t.Fatal("expected an error")
			}
			if data, err := os.ReadFile(previous); err != nil || string(data) != "previous" {
				t.Errorf("previous install = %q, %v; want it untouched", data, err)
			}
			if names := dirNames(t, targetDir); !slices.Equal(names, []string{"yt-dlp"}) {
				t.Errorf("target directory holds %v, want only yt-dlp", names)
			}
			if names := dirNames(t, root); !slices.Equal(names, []string{"bin"}) {
				t.Errorf("extraction escaped into %v", names)
			}
		})
	}
}

func TestInstallYtDlpRejectsUnsupportedPlatform(t *testing.T) {
	requested := serveReleases(t, nil)
	if err := InstallYtDlpForPlatform(context.Background(), "freebsd", "amd64", t.TempDir()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
	if got := requested(); len(got) != 0 {
		t.Errorf("requested %v for an unsupported platform", got)
	}
}

// Unpacked builds do not support yt-dlp -U, so installing and updating both
// download the build and never run yt-dlp.
func TestYtDlpInstallerDownloadsForInstallAndUpdate(t *testing.T) {
	asset, err := ytDlpAssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no unpacked yt-dlp build for this platform: %v", err)
	}
	requested := serveReleases(t, map[string][]byte{asset.archive: unpackedBuild(t, asset.executable)})
	var ran []string
	oldRunner := packageManagerRunner
	t.Cleanup(func() { packageManagerRunner = oldRunner })
	packageManagerRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, errors.New("unexpected command")
	}

	installer := YtDlp()
	for _, op := range []struct {
		name string
		run  func(context.Context, string) error
	}{{"install", installer.Install}, {"update", installer.Update}} {
		t.Run(op.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			targetDir := t.TempDir()
			before := len(requested())
			if err := op.run(context.Background(), targetDir); err != nil {
				t.Fatal(err)
			}
			if got := requested()[before:]; len(got) != 1 || !strings.HasSuffix(got[0], "/"+asset.archive) {
				t.Errorf("requested %v, want one download of %s", got, asset.archive)
			}
			if len(ran) != 0 {
				t.Errorf("ran %v, want no commands", ran)
			}
			if runtime.GOOS == "windows" {
				return
			}
			out, err := exec.Command(filepath.Join(targetDir, "yt-dlp", "yt-dlp")).Output()
			if err != nil || strings.TrimSpace(string(out)) != "2026.10.26" {
				t.Errorf("running the installed entry point = %q, %v", out, err)
			}
		})
	}
}

func TestReplaceDirRestoresPreviousInstallWhenMoveFails(t *testing.T) {
	targetDir := t.TempDir()
	dest := filepath.Join(targetDir, "yt-dlp")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "yt-dlp"), []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceDir(filepath.Join(targetDir, ".missing-staging"), dest); err == nil {
		t.Fatal("expected an error for a missing staged directory")
	}
	if data, err := os.ReadFile(filepath.Join(dest, "yt-dlp")); err != nil || string(data) != "previous" {
		t.Errorf("previous install = %q, %v; want it restored", data, err)
	}
	if names := dirNames(t, targetDir); !slices.Equal(names, []string{"yt-dlp"}) {
		t.Errorf("target directory holds %v, want only yt-dlp", names)
	}
}

// An installation killed before its cleanup leaves dot-prefixed downloads and
// staging trees behind; a later installation removes them unless they are
// recent enough to belong to an installation still running.
func TestInstallYtDlpRemovesStaleLeftovers(t *testing.T) {
	serveReleases(t, map[string][]byte{"yt-dlp_macos.zip": unpackedBuild(t, "yt-dlp_macos")})
	targetDir := t.TempDir()
	old := time.Now().Add(-2 * staleLeftoverAge)
	for _, name := range []string{".yt-dlp-1.zip", ".yt-dlp-2/_internal/lib.so", ".yt-dlp-old-3/yt-dlp/yt-dlp", ".yt-dlp-recent/lib.so", ".other-tool-4/lib.so"} {
		path := filepath.Join(targetDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("leftover"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".yt-dlp-1.zip", ".yt-dlp-2", ".yt-dlp-old-3", ".other-tool-4"} {
		if err := os.Chtimes(filepath.Join(targetDir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := InstallYtDlpForPlatform(context.Background(), "darwin", "arm64", targetDir); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, targetDir); !slices.Equal(names, []string{".other-tool-4", ".yt-dlp-recent", "yt-dlp"}) {
		t.Errorf("target directory holds %v, want stale yt-dlp leftovers removed and the rest kept", names)
	}
}
