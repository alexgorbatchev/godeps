---
created_on: 2026-08-23 13:25
last_modified: 2026-10-21 19:27
status: current
---

# AGENTS.md — godeps

Operational guidelines, architecture, and developer interface specifications for AI agents interacting with or maintaining `godeps`.

---

## 1. Development & Verification Commands

All development tasks must be executed using `just` recipes:

```bash
# Run full unit test suite with race detector and coverage check
just test

# Run static code analysis
just vet

# Format all Go source files
just fmt

# Verify module hygiene
just tidy
```

---

## 2. Architecture & Design Principles

`godeps` is a zero-configuration, token-free external dependency orchestrator for Go CLI applications.

### Core Package Components
- **`godeps.go`**: Core orchestration engine (`Manager`, `Config`, `Dependency`, `DependencyReport`, `Ensure`, `Verify`).
- **`compare.go`**: Multi-format version parser and comparator powered by `hashicorp/go-version` supporting SemVer, CalVer (`YYYY.MM.DD`), development builds (`dev`, `git-master`), and output banner stripping.
- **`managed.go`**: XDG Base Directory resolution (`$XDG_DATA_HOME/<appName>/bin`) and dynamic runtime `$PATH` injection.
- **`installer.go`**: Rate-limit-free GitHub release resolver (`HEAD /releases/latest` redirect headers), `.tar.gz`/`.zip` archive extractors, standalone binary downloaders, and OS package manager wrappers (`brew`, `apt-get`, `pacman`, `dnf`, `winget`, `choco`).
- **`sanitize.go`**: External command stderr cleaner stripping multiline Python tracebacks, Go panics, and Node.js stack traces.
- **`selfupdate.go`**: In-place running application self-upgrades without GitHub token requirements.

---

## 3. Operating Boundaries & Rules

### Always
- **Mandatory Testing & Coverage**: Any time code is changed such that results from running that code are changed, a test file must be changed as well, and **90% code coverage is required** (verified via `just test`).
- **Standard Library First**: Maintain zero heavy external dependencies. Use standard library primitives for HTTP, archives, and process execution.
- **Rate-Limit-Free HTTP Resolution**: Never query the GitHub REST API (`/repos/.../releases`) for release discovery; always use HTTP `HEAD /releases/latest` redirect headers to avoid unauthenticated rate limiting.
- **Fail Closed**: A dependency is satisfied only when its version was read and compared successfully. Empty, unparseable, or unverifiable versions and installations that still do not satisfy the requirement are failures.
- **Error Kinds Over Messages**: Classify errors with `errors.Is`/`errors.As`; never infer an error kind from message substrings.

### Ask First
- Adding third-party Go module dependencies beyond `hashicorp/go-version`.
- Modifying default XDG directory layout or PATH resolution semantics.

### Never
- Never commit compiled binaries or test artifacts to the repository.
- Never use heredocs in scripts or commands.
- Never write to standard output from the library; consumers print machine-readable results there. Messages and prompts go to `EnsureOptions.Output` (default standard error).
- Never treat EOF or non-interactive closed stdin as an acceptance of interactive prompts (always default to `[y/N]` rejection unless `AutoInstall: true`).
