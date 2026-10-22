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

// CommandRunner executes an external command. Its errors must wrap the
// underlying exec error with %w so a missing executable stays recognizable.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultRunner is the default CommandRunner using os/exec with sanitized stderr errors.
func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
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

// IsAgentMode checks if AGENT=1 or AGENT=true is set in the environment.
func IsAgentMode() bool {
	val := strings.TrimSpace(os.Getenv("AGENT"))
	return val == "1" || strings.EqualFold(val, "true")
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

// Ensure checks dependencies, injects managed PATH, and installs missing or
// outdated dependencies when AutoInstall is set or an interactive user agrees.
// It succeeds only when every dependency is satisfied afterward.
func (m *Manager) Ensure(ctx context.Context, opts ...EnsureOptions) error {
	_ = InitManagedPath(m.appName)

	reports, err := m.Verify(ctx)
	if err == nil {
		return nil
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
	// example when its latest release is older than required or an older copy
	// earlier in PATH still shadows the managed directory.
	if _, err := m.Verify(ctx); err != nil {
		return fmt.Errorf("dependencies remain unsatisfied after installation: %w", err)
	}
	fmt.Fprintf(out, "✓ Successfully installed: %s\n", strings.Join(installed, ", "))
	return nil
}

// Verify runs version checks across all declared dependencies.
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
				versionArgs := dep.VersionArgs
				if len(versionArgs) == 0 {
					versionArgs = []string{"--version"}
				}

				out, err = m.runner(ctx, dep.Name, versionArgs...)
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
				parser := dep.ParseVersion
				if parser == nil {
					parser = DefaultVersionParser
				}
				versionStr := parser(dep.Name, string(out))
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
	return reports, firstErr
}

// Install installs a single dependency by name using its declared Installer.
func (m *Manager) Install(ctx context.Context, depName string) error {
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
			return dep.Installer.Install(ctx, binDir)
		}
	}

	return fmt.Errorf("unknown dependency %q", depName)
}

// Update updates a single dependency by name using its declared Installer.
func (m *Manager) Update(ctx context.Context, depName string) error {
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
			return dep.Installer.Update(ctx, binDir)
		}
	}

	return fmt.Errorf("unknown dependency %q", depName)
}

// InstallUnsatisfied installs missing dependencies and updates outdated ones.
func (m *Manager) InstallUnsatisfied(ctx context.Context) ([]string, error) {
	_ = InitManagedPath(m.appName)

	reports, _ := m.Verify(ctx)
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
	_ = InitManagedPath(m.appName)

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
