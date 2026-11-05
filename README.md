# godeps

> Embeddable, zero-token external binary dependency manager and self-update library for Go CLI applications.

`godeps` enables Go command-line tools to declare external binary prerequisites (such as `yt-dlp`, `ffmpeg`, or custom CLI helpers), verify version constraints across SemVer and CalVer, auto-install missing/outdated binaries into managed application directories without GitHub API rate limits, and sanitize error outputs from external processes.

---

## What It Does

- **Declarative Prerequisites**: Declare required external CLI tools with minimum versions and install strategies.
- **Multi-Format Version Checking**: Compares standard SemVer (`1.4.0`), CalVer (`2024.08.01`), and development builds (`dev`, `git-master`), stripping tool output banners automatically. Empty or unrecognizable versions never satisfy a requirement.
- **Managed Directory & Runtime PATH Injection**: Downloads binaries into isolated user data directories (`$XDG_DATA_HOME/<app>/bin` / `~/.local/share/<app>/bin`) and adds them to the runtime process `$PATH`. A copy installed elsewhere on `PATH` that meets the minimum version always wins; the managed copy is used only when that copy is missing or too old.
- **Rate-Limit-Free GitHub Release Resolution**: Resolves latest releases via HTTP `HEAD` redirect headers without consuming GitHub API rate limits.
- **Built-in Archive Extractors**: Unpacks `.tar.gz`, `.zip`, and raw executable files with automatic permission management (`0755`).
- **Graceful Cancellation**: Commands run through `DefaultRunner` or `CommandContext` stop with SIGTERM to their whole process group when the context ends, and receive SIGKILL only after a 5-second grace period.
- **External Stderr Sanitizer**: Strips raw multiline Python tracebacks, Go panics, and Node.js stack frames, surfacing clean error messages.
- **In-Place Self-Updates**: Upgrades the running CLI application directly from GitHub release assets.
- **Agent-Ready & Safe Prompts**: Automatically disables interactive prompts when `AGENT` is `1`, `true`, or `yes` (any case), and defaults interactive prompts to `[y/N]` rejection on Enter/EOF. Prompts and progress go to standard error (or `EnsureOptions.Output`), never standard output, so they cannot corrupt a program's machine-readable results. A cancelled context ends `Ensure`, `Verify`, and `InstallUnsatisfied` with the context's error instead of prompting or installing.
- **Verified Installation**: `Ensure` and `InstallUnsatisfied` install missing tools and update outdated ones, and `Ensure` fails unless every dependency is satisfied afterward.

---

## Installation

```bash
go get github.com/alexgorbatchev/godeps/v2
```

---

## Quick Start

### 1. Declare Dependencies and Ensure at Startup

```go
package main

import (
	"context"
	"log"

	"github.com/alexgorbatchev/godeps/v2"
)

var depsManager = godeps.New(godeps.Config{
	AppName: "mycli",
	Dependencies: []godeps.Dependency{
		{
			Name:       "fetch-track",
			MinVersion: "1.4.0",
			InstallURL: "https://github.com/alexgorbatchev/fetch-track-cli",
			Installer:  godeps.GitHubReleaseGoBinary("alexgorbatchev", "fetch-track-cli", "fetch-track"),
		},
		{
			Name:       "yt-dlp",
			MinVersion: "2024.08.01",
			InstallURL: "https://github.com/yt-dlp/yt-dlp",
			Installer:  godeps.YtDlp(),
		},
		{
			Name:       "ffmpeg",
			MinVersion: "4.4",
			InstallURL: "https://ffmpeg.org/download.html",
			VersionArgs: []string{"-version"},
			Installer:  godeps.SystemPackageManager("ffmpeg"),
		},
	},
})

func main() {
	ctx := context.Background()

	// Puts ~/.local/share/mycli/bin on the process PATH and verifies all prerequisites
	if err := depsManager.Ensure(ctx, godeps.EnsureOptions{
		AutoInstall: false, // Set true to bypass interactive [y/N] prompt
		// Output and Input default to os.Stderr and os.Stdin.
	}); err != nil {
		log.Fatalf("Prerequisites error: %v", err)
	}

	// Dependencies are operational and available in PATH
}
```

### 2. Verify Dependencies and Print Reports

`Verify` checks the copy each name resolves to on the current `PATH`, so call `depsManager.InitManagedPath(ctx)` first (as `Ensure` does) when the managed directory should replace outdated copies. A dependency whose executable does not exist reports `Installed: false`. Custom `CommandRunner`s must wrap the underlying exec error with `%w` so a missing executable stays distinguishable from a failed version check.

```go
reports, err := depsManager.Verify(ctx)
for _, r := range reports {
	if r.Satisfied {
		fmt.Println("OK:", r.Summary())
	} else {
		fmt.Println("FAIL:", r.Summary())
	}
}
```

### 3. In-Place Self-Upgrade

```go
// Upgrades the current executable to the latest release on GitHub
newVersion, err := godeps.UpgradeSelf(ctx, "alexgorbatchev", "mycli", "1.0.0")
if err != nil {
	log.Fatalf("Self-update failed: %v", err)
}
fmt.Printf("Upgraded to version %s\n", newVersion)
```

### 4. Clean Stderr / Stack-Trace Sanitization

```go
cleanError := godeps.SanitizeStderr(rawStderrOutput)
```

---

## Built-In Installer Strategies

| Strategy | Description |
| :--- | :--- |
| `godeps.GitHubReleaseGoBinary(owner, repo, binName)` | Downloads `.tar.gz` (Linux/macOS) or `.zip` (Windows) release assets from GitHub and extracts `binName`. |
| `godeps.YtDlp()` | Downloads yt-dlp's unpacked build for the platform (`yt-dlp_macos.zip`, `yt-dlp_linux.zip`, `yt-dlp_linux_aarch64.zip`, `yt-dlp_win.zip`, `yt-dlp_win_x86.zip`, `yt-dlp_win_arm64.zip`) into the package directory `bin/yt-dlp/`. Install and update both download the latest build. Other platforms return `godeps.ErrUnsupportedPlatform`. |
| `godeps.SystemPackageManager(packageName)` | Invokes host package managers (`brew`, `apt-get`, `pacman`, `dnf`, `winget`, `choco`). |
| `godeps.InstallerFunc(fn)` | Custom installation function implementing `func(ctx context.Context, targetDir string) error`. |

---

## Managed Directory Layout

| Path | Contents |
| :--- | :--- |
| `<data>/<app>/bin/` | Single-file executables, such as those from `GitHubReleaseGoBinary` |
| `<data>/<app>/bin/<name>/` | A package directory: a build that runs from a directory tree, with its entry point at `bin/<name>/<name>` (`<name>.exe` on Windows). `YtDlp()` installs `bin/yt-dlp/`. |

`<data>` is `$XDG_DATA_HOME`, then `%LOCALAPPDATA%` on Windows, then `~/.local/share`. The bin directory and each package directory are separate `PATH` entries. Subdirectories whose names start with a dot hold in-progress downloads and are never put on `PATH`.

yt-dlp's unpacked build runs directly from `bin/yt-dlp/` and creates nothing in the system temporary directory. An installation is extracted next to the package directory and moved into place only once complete, so a failed download or extraction leaves the previous installation working. Archive entries and links that would land outside the package directory fail the installation. Symbolic links must resolve inside the completed extraction; dangling links and loops also fail installation. Targets may appear later in the archive. Downloads and staging trees left by an installation that was killed are removed by the next installation once they are an hour old. Installing replaces an earlier one-file `bin/yt-dlp` (or `bin/yt-dlp.exe`) executable.

---

## PATH Precedence

| Function | Effect on `PATH` |
| :--- | :--- |
| `godeps.InitManagedPath(appName)` | Appends the bin directory and every package directory that is not already on `PATH`. Copies elsewhere on `PATH` always win, and managed copies fill in only for missing executables. Directories already on `PATH` keep their position. |
| `manager.InitManagedPath(ctx)` | Places each dependency's managed directory by version. A copy elsewhere on `PATH` that meets `MinVersion` wins. When that copy is older than `MinVersion` and the managed copy is not, the managed directory moves ahead of the rest of `PATH`. Otherwise the managed directories go last. |

`Ensure`, `InstallUnsatisfied`, `Install`, and `Update` call `manager.InitManagedPath`, so a freshly installed copy is used immediately when it should be. Version checks run only for dependencies with both a managed copy and another copy on `PATH`, and they pass the runner absolute paths.

A package directory affects only its own dependency. Single-file executables share the bin directory: when it moves ahead for one dependency, its other executables also take precedence over the copies they shadow, and `Verify` still checks whatever each name resolves to.

---

## Subprocess Cancellation

`godeps.CommandContext(ctx, name, args...)` returns an `*exec.Cmd` like `exec.CommandContext`, and `DefaultRunner` uses it. On Unix, the command runs in its own process group. When `ctx` ends, the whole group receives SIGTERM, so tools can clean up and their own subprocesses stop too. Members still running 5 seconds later receive SIGKILL. After a cancellation, `Wait` (and so `Run`, `Output`, and `DefaultRunner`) returns only once every member of the group has exited or been sent SIGKILL, so your program can exit right away without leaving any of them running. `Wait` also stops waiting for output held open by subprocesses outside the group 5 seconds after that.

The process group is not the terminal's foreground group, so Ctrl+C reaches only your program. Cancel the context on interrupt, for example with `signal.NotifyContext`, or the command keeps running after your program exits. Use it for non-interactive commands, since a background process group cannot read from the terminal.

On Windows, which has no SIGTERM, cancellation kills the process as `exec.CommandContext` does.

`SystemPackageManager` installers, and `InstallPackage` with a nil runner, are the exception: package managers may prompt through the terminal, for example for a sudo password, so they run in your program's process group. Cancellation sends them SIGTERM and kills them 5 seconds later.

---

## Development

Contributor commands are documented in [AGENTS.md](AGENTS.md#1-development--verification-commands).

---

## License

MIT License. See [LICENSE](LICENSE) for details.
