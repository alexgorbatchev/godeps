package godeps

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// CommandRunner executes an external command. name is a bare executable name
// resolved through PATH, or an absolute path when a Manager compares a
// dependency's managed copy with another copy on PATH. Its errors must wrap the
// underlying exec error with %w so a missing executable stays recognizable.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultRunner is the default CommandRunner. It runs the command with
// CommandContext, so cancellation terminates it gracefully, and returns errors
// carrying the command's sanitized standard error.
func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCaptured(CommandContext(ctx, name, args...))
}

// packageManagerRunner runs the commands of SystemPackageManager installers.
var packageManagerRunner CommandRunner = runPackageManager

// runPackageManager runs a package manager in the terminal's process group, so
// it can prompt for input such as a sudo password, which a background process
// group cannot read. Cancellation still sends SIGTERM first.
func runPackageManager(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCaptured(foregroundCommandContext(ctx, name, args...))
}

// runCaptured runs cmd and returns its standard output, or an error carrying
// its sanitized standard error.
func runCaptured(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if stderr.Len() > 0 {
			cleanErr := SanitizeStderr(stderr.String())
			if cleanErr != "" {
				return nil, fmt.Errorf("%w: %s", err, cleanErr)
			}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// IsAgentMode reports whether AGENT is 1, true, or yes, ignoring case and
// surrounding whitespace.
func IsAgentMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// Cache defines optional key-value caching for dependency verification.
type Cache interface {
	Get(key string, target any) bool
	Put(key string, val any) error
	Delete(key string) error
}

// Dependency defines a prerequisite tool requirement.
type Dependency struct {
	Name         string
	MinVersion   string
	InstallURL   string
	VersionArgs  []string
	ParseVersion func(name, output string) string
	Installer    Installer
}

func (d Dependency) versionArgs() []string {
	if len(d.VersionArgs) == 0 {
		return []string{"--version"}
	}
	return d.VersionArgs
}

func (d Dependency) parseVersion(output []byte) string {
	parser := d.ParseVersion
	if parser == nil {
		parser = DefaultVersionParser
	}
	return parser(d.Name, string(output))
}

// satisfiedBy reports whether the executable at path reports a version that
// meets dep's minimum.
func (m *Manager) satisfiedBy(ctx context.Context, dep Dependency, path string) bool {
	out, err := m.runner(ctx, path, dep.versionArgs()...)
	if err != nil {
		return false
	}
	version := dep.parseVersion(out)
	return version != "" && CompareVersions(version, dep.MinVersion) == nil
}

// DependencyReport summarizes the status of a checked dependency.
type DependencyReport struct {
	Name            string `json:"name"`
	MinVersion      string `json:"minVersion"`
	DetectedVersion string `json:"detectedVersion"`
	Installed       bool   `json:"installed"`
	Satisfied       bool   `json:"satisfied"`
	Error           string `json:"error,omitempty"`
}

// Summary returns a human-readable summary of the dependency state.
func (r DependencyReport) Summary() string {
	if !r.Satisfied {
		if r.Installed && r.DetectedVersion != "" {
			return fmt.Sprintf("%s (installed %s, required >= %s)", r.Name, r.DetectedVersion, r.MinVersion)
		}
		return fmt.Sprintf("%s (not installed, required >= %s)", r.Name, r.MinVersion)
	}
	return fmt.Sprintf("%s (version %s, min %s)", r.Name, r.DetectedVersion, r.MinVersion)
}

// Config defines the setup for a Manager instance.
type Config struct {
	AppName      string
	Dependencies []Dependency
	Runner       CommandRunner
	Cache        Cache
}

// Manager orchestrates dependency checks, managed PATH injection, and installations.
type Manager struct {
	appName      string
	dependencies []Dependency
	runner       CommandRunner
	cache        Cache
}

// New creates a new Manager instance.
func New(cfg Config) *Manager {
	runner := cfg.Runner
	if runner == nil {
		runner = DefaultRunner
	}
	appName := cfg.AppName
	if appName == "" {
		appName = "godeps"
	}
	return &Manager{
		appName:      appName,
		dependencies: cfg.Dependencies,
		runner:       runner,
		cache:        cfg.Cache,
	}
}

// EnsureOptions configures the Ensure execution.
type EnsureOptions struct {
	// AutoInstall installs missing and outdated dependencies without prompting.
	AutoInstall bool
	// Output receives progress messages and the installation prompt. It defaults
	// to os.Stderr, keeping standard output free for the program's own results.
	Output io.Writer
	// Input answers the installation prompt. It defaults to os.Stdin.
	Input io.Reader
}

// Ensure places the managed directories on PATH with Manager.InitManagedPath,
// checks dependencies, and installs missing or outdated dependencies when
// AutoInstall is set or an interactive user agrees. It succeeds only when
// every dependency is satisfied afterward, and returns ctx.Err() without
// prompting or installing once ctx is done.
func (m *Manager) Ensure(ctx context.Context, opts ...EnsureOptions) error {
	_ = m.InitManagedPath(ctx)

	reports, err := m.Verify(ctx)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	var o EnsureOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	out := o.Output
	if out == nil {
		out = os.Stderr
	}
	in := o.Input
	if in == nil {
		in = os.Stdin
	}

	var unsatisfied []string
	for _, r := range reports {
		if !r.Satisfied {
			unsatisfied = append(unsatisfied, r.Summary())
		}
	}

	if !o.AutoInstall {
		if IsAgentMode() {
			return err
		}
		fmt.Fprintf(out, "\nMissing or outdated dependencies: %s\nWould you like to auto-install them to managed directory? [y/N]: ", strings.Join(unsatisfied, ", "))
		ans, readErr := bufio.NewReader(in).ReadString('\n')
		if readErr != nil && len(ans) == 0 {
			return err
		}
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			return err
		}
	}

	fmt.Fprintf(out, "Installing missing/outdated dependencies: %s...\n", strings.Join(unsatisfied, ", "))
	installed, installErr := m.InstallUnsatisfied(ctx)
	if installErr != nil {
		return fmt.Errorf("auto-installing dependencies: %w", installErr)
	}
	// An installer can succeed without providing a satisfying version, for
	// example when its latest release is older than required.
	if _, err := m.Verify(ctx); err != nil {
		return fmt.Errorf("dependencies remain unsatisfied after installation: %w", err)
	}
	fmt.Fprintf(out, "✓ Successfully installed: %s\n", strings.Join(installed, ", "))
	return nil
}

// Verify runs version checks across all declared dependencies. When ctx is
// done, it returns ctx.Err(), since interrupted checks prove nothing.
func (m *Manager) Verify(ctx context.Context) ([]DependencyReport, error) {
	deps := m.dependencies
	reports := make([]DependencyReport, len(deps))
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup

	isAgent := IsAgentMode()

	for i, dep := range deps {
		wg.Add(1)
		go func(idx int, dep Dependency) {
			defer wg.Done()

			report := DependencyReport{
				Name:       dep.Name,
				MinVersion: dep.MinVersion,
			}

			var out []byte
			var err error

			cacheKey := fmt.Sprintf("godeps_%s.json", dep.Name)
			var cachedStr string
			if m.cache != nil && m.cache.Get(cacheKey, &cachedStr) && cachedStr != "" {
				out = []byte(cachedStr)
			} else {
				out, err = m.runner(ctx, dep.Name, dep.versionArgs()...)
				if err == nil && len(out) > 0 && m.cache != nil {
					_ = m.cache.Put(cacheKey, string(out))
				}
			}

			if err != nil {
				if isNotFound(err) {
					report.Installed = false
					report.Satisfied = false
					if isAgent {
						report.Error = fmt.Sprintf("%s is missing in $PATH. Ask user for confirmation to install %s version %s or newer.", dep.Name, dep.Name, dep.MinVersion)
					} else {
						report.Error = fmt.Sprintf("%s is missing in $PATH, please install %s (%s)", dep.Name, dep.Name, dep.InstallURL)
					}
				} else {
					report.Installed = false
					report.Satisfied = false
					report.Error = fmt.Sprintf("failed to check %s version: %v", dep.Name, err)
				}
			} else {
				report.Installed = true
				versionStr := dep.parseVersion(out)
				report.DetectedVersion = versionStr

				if versionStr == "" {
					report.Satisfied = false
					report.Error = fmt.Sprintf("could not parse %s version output", dep.Name)
				} else if compErr := CompareVersions(versionStr, dep.MinVersion); compErr != nil {
					report.Satisfied = false
					if isAgent {
						report.Error = fmt.Sprintf("%s version %s in $PATH is outdated. Ask user for confirmation to update %s to version %s or newer.", dep.Name, versionStr, dep.Name, dep.MinVersion)
					} else {
						report.Error = fmt.Sprintf("%s in $PATH (version %s) is outdated, must be version %s or newer", dep.Name, versionStr, dep.MinVersion)
					}
				} else {
					report.Satisfied = true
				}
			}

			mu.Lock()
			reports[idx] = report
			if !report.Satisfied && firstErr == nil {
				firstErr = errors.New(report.Error)
			}
			mu.Unlock()
		}(i, dep)
	}

	wg.Wait()
	// A check killed by cancellation says nothing about the dependency.
	if err := ctx.Err(); err != nil {
		return reports, err
	}
	return reports, firstErr
}

// Install installs a single dependency by name using its declared Installer,
// then refreshes PATH with Manager.InitManagedPath so the new copy is used
// when it should be.
func (m *Manager) Install(ctx context.Context, depName string) error {
	return m.runInstaller(ctx, depName, Installer.Install)
}

// Update updates a single dependency by name using its declared Installer,
// then refreshes PATH with Manager.InitManagedPath.
func (m *Manager) Update(ctx context.Context, depName string) error {
	return m.runInstaller(ctx, depName, Installer.Update)
}

func (m *Manager) runInstaller(ctx context.Context, depName string, run func(Installer, context.Context, string) error) error {
	binDir, err := EnsureManagedBinDir(m.appName)
	if err != nil {
		return err
	}

	if m.cache != nil {
		_ = m.cache.Delete(fmt.Sprintf("godeps_%s.json", depName))
	}

	for _, dep := range m.dependencies {
		if strings.EqualFold(dep.Name, depName) {
			if dep.Installer == nil {
				return fmt.Errorf("no installer configured for dependency %q", depName)
			}
			if err := run(dep.Installer, ctx, binDir); err != nil {
				return err
			}
			return m.InitManagedPath(ctx)
		}
	}

	return fmt.Errorf("unknown dependency %q", depName)
}

// InstallUnsatisfied places the managed directories on PATH with
// Manager.InitManagedPath, then installs missing dependencies and updates
// outdated ones.
func (m *Manager) InstallUnsatisfied(ctx context.Context) ([]string, error) {
	_ = m.InitManagedPath(ctx)

	reports, _ := m.Verify(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var installed []string
	var errs []error

	for _, r := range reports {
		if !r.Satisfied {
			install := m.Install
			if r.Installed {
				install = m.Update
			}
			if err := install(ctx, r.Name); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
			} else {
				installed = append(installed, r.Name)
			}
		}
	}

	if len(errs) > 0 {
		return installed, errors.Join(errs...)
	}

	return installed, nil
}

// UpdateAll updates all declared dependencies.
func (m *Manager) UpdateAll(ctx context.Context) ([]string, error) {
	var updated []string
	var errs []error

	for _, dep := range m.dependencies {
		if err := m.Update(ctx, dep.Name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dep.Name, err))
		} else {
			updated = append(updated, dep.Name)
		}
	}

	if len(errs) > 0 {
		return updated, errors.Join(errs...)
	}

	return updated, nil
}

// isNotFound reports an executable that does not exist, as opposed to an
// installed tool whose version check failed with a message mentioning a
// missing file. Runners must wrap the exec error with %w to keep its kind.
func isNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}
