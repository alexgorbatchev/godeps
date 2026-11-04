---
created_on: 2026-08-23 13:25
last_modified: 2026-11-04 11:10
status: current
---

# AGENTS.md — godeps

Operational guidelines, architecture, and developer interface specifications for AI agents interacting with or maintaining `godeps`.

---

## 1. Development & Verification Commands

All development tasks must be executed using `just` recipes:

```bash
# Run full unit test suite with race detector; fails below 90% statement coverage
just test

# Build for the host and for Windows
just build

# Run static code analysis for the host and for Windows
just vet

# Format all Go source files
just fmt

# Verify module hygiene
just tidy
```

CI (`.github/workflows/ci.yml`) runs `just tidy`, `just build`, `just vet`, and `just test` on Linux and macOS for pushes and pull requests to `main`.

---

## 2. Architecture & Design Principles

`godeps` is a zero-configuration, token-free external dependency orchestrator for Go CLI applications.

### Core Package Components
- **`godeps.go`**: Core orchestration engine (`Manager`, `Config`, `Dependency`, `DependencyReport`, `Ensure`, `Verify`).
- **`compare.go`**: Multi-format version parser and comparator powered by `hashicorp/go-version` supporting SemVer, CalVer (`YYYY.MM.DD`), development builds (`dev`, `git-master`), and output banner stripping.
- **`managed.go`**: XDG Base Directory resolution (`$XDG_DATA_HOME/<appName>/bin`) and runtime `$PATH` placement. The managed directories are `bin/` (single-file executables) and package directories `bin/<name>/` (directory-tree builds with the entry point `bin/<name>/<name>`); dot-prefixed subdirectories are installer staging and never go on `PATH`. `InitManagedPath(appName)` appends missing managed directories. `Manager.InitManagedPath(ctx)` keeps a copy elsewhere on `PATH` that meets `MinVersion` ahead of the managed copy, and moves a dependency's managed directory to the front only when the other copy is below `MinVersion` and the managed copy is not.
- **`command.go`**, **`command_unix.go`**, **`command_other.go`**: `CommandContext`, used by `DefaultRunner`. On Unix the child runs in its own process group; cancellation sends SIGTERM to the group, then SIGKILL to surviving members after `terminationGracePeriod` (5 seconds), which also bounds `WaitDelay`. Other platforms keep the default kill.
- **`ytdlp.go`**: Installs yt-dlp's unpacked (PyInstaller one-folder) release zip for the platform into `bin/yt-dlp/`, renaming the archive's executable to `yt-dlp`/`yt-dlp.exe`. It stages next to the package directory, swaps it into place, and removes an obsolete one-file build. Extraction writes only through an `os.Root` on the staging directory and rejects link targets outside the archive root. Each install first removes `.yt-dlp-*` leftovers older than an hour from killed installs. Updating re-downloads; never use `yt-dlp -U`, which unpacked builds do not support. Never install the one-file builds, which unpack into the system temporary directory on every run and leak it when killed.
- **`installer.go`**: Rate-limit-free GitHub release resolver (`HEAD /releases/latest` redirect headers), `.tar.gz`/`.zip` single-binary extractors, standalone binary downloaders, and OS package manager wrappers (`brew`, `apt-get`, `pacman`, `dnf`, `winget`, `choco`).
- **`sanitize.go`**: External command stderr cleaner stripping multiline Python tracebacks, Go panics, and Node.js stack traces.
- **`selfupdate.go`**: In-place running application self-upgrades without GitHub token requirements.

---

## 3. Operating Boundaries & Rules

### Always
- **Mandatory Testing & Coverage**: Any time code is changed such that results from running that code are changed, a test file must be changed as well, and **90% code coverage is required** (verified via `just test`).
- **Standard Library First**: Maintain zero heavy external dependencies. Use standard library primitives for HTTP, archives, and process execution.
- **Rate-Limit-Free HTTP Resolution**: Never query the GitHub REST API (`/repos/.../releases`) for release discovery; always use HTTP `HEAD /releases/latest` redirect headers to avoid unauthenticated rate limiting.
- **Fail Closed**: A dependency is satisfied only when its version was read and compared successfully. Empty, unparseable, or unverifiable versions and installations that still do not satisfy the requirement are failures. A done context makes `Verify`, `Ensure`, and `InstallUnsatisfied` return `ctx.Err()`; never prompt or install after cancellation.
- **Graceful Subprocesses**: Start every subprocess the package runs through `CommandContext`; never use bare `exec.CommandContext`, whose cancellation SIGKILLs only the direct child. The one exception is package managers (`SystemPackageManager`, and `InstallPackage` with a nil runner), which run through `runPackageManager` in the caller's process group so they can prompt through the terminal (for example for a sudo password); cancellation sends SIGTERM to the process and kills it after the grace period.
- **Semantic Import Versioning**: The module path is `github.com/alexgorbatchev/godeps/v2`. A release that removes or incompatibly changes exported API, or changes documented behavior consumers rely on, is a new major version: bump the `/vN` module path suffix and the README import lines, and tag `vN.0.0`.
- **Error Kinds Over Messages**: Classify errors with `errors.Is`/`errors.As`; never infer an error kind from message substrings.

### Ask First
- Adding third-party Go module dependencies beyond `hashicorp/go-version`.
- Modifying default XDG directory layout or PATH resolution semantics.

### Never
- Never commit compiled binaries or test artifacts to the repository.
- Never use heredocs in scripts or commands.
- Never write to standard output from the library; consumers print machine-readable results there. Messages and prompts go to `EnsureOptions.Output` (default standard error).
- Never treat EOF or non-interactive closed stdin as an acceptance of interactive prompts (always default to `[y/N]` rejection unless `AutoInstall: true`).
