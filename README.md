# godeps

> Embeddable, zero-token external binary dependency manager and self-update library for Go CLI applications.

`godeps` enables Go command-line tools to declare external binary prerequisites (such as `yt-dlp`, `ffmpeg`, or custom CLI helpers), verify version constraints across SemVer and CalVer, auto-install missing/outdated binaries into managed application directories without GitHub API rate limits, and sanitize error outputs from external processes.

---

## What It Does

- **Declarative Prerequisites**: Declare required external CLI tools with minimum versions and install strategies.
- **Multi-Format Version Checking**: Compares standard SemVer (`1.4.0`), CalVer (`2024.08.01`), and development builds (`dev`, `git-master`), stripping tool output banners automatically.
- **Managed Directory & Runtime PATH Injection**: Downloads binaries into isolated user data directories (`$XDG_DATA_HOME/<app>/bin` / `~/.local/share/<app>/bin`) and prepends the directory to the runtime process `$PATH`.
- **Rate-Limit-Free GitHub Release Resolution**: Resolves latest releases via HTTP `HEAD` redirect headers without consuming GitHub API rate limits.
- **Built-in Archive Extractors**: Unpacks `.tar.gz`, `.zip`, and raw executable files with automatic permission management (`0755`).
- **External Stderr Sanitizer**: Strips raw multiline Python tracebacks, Go panics, and Node.js stack frames, surfacing clean error messages.
- **In-Place Self-Updates**: Upgrades the running CLI application directly from GitHub release assets.
- **Agent-Ready & Safe Prompts**: Automatically disables interactive prompts when `AGENT=1` is set, and defaults interactive prompts to `[y/N]` rejection on Enter/EOF.

---

## Installation

```bash
go get github.com/alexgorbatchev/godeps
```

---

## Quick Start

### 1. Declare Dependencies and Ensure at Startup

```go
package main

import (
	"context"
	"log"

	"github.com/alexgorbatchev/godeps"
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

	// Injects ~/.local/share/mycli/bin to process PATH and verifies all prerequisites
	if err := depsManager.Ensure(ctx, godeps.EnsureOptions{
		AutoInstall: false, // Set true to bypass interactive [y/N] prompt
	}); err != nil {
		log.Fatalf("Prerequisites error: %v", err)
	}

	// Dependencies are operational and available in PATH
}
```

### 2. Verify Dependencies and Print Reports

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
| `godeps.YtDlp()` | Downloads standalone OS/arch-specific binaries (`yt-dlp_macos`, `yt-dlp_linux`, `yt-dlp_linux_aarch64`, `yt-dlp.exe`) containing embedded Python runtimes. |
| `godeps.SystemPackageManager(packageName)` | Invokes host package managers (`brew`, `apt-get`, `pacman`, `dnf`, `winget`, `choco`). |
| `godeps.InstallerFunc(fn)` | Custom installation function implementing `func(ctx context.Context, targetDir string) error`. |

---

## Development

```bash
# Run tests with race detection and coverage verification
just test

# Static analysis
just vet

# Format code
just fmt
```

---

## License

MIT License. See [LICENSE](LICENSE) for details.
