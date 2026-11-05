package godeps

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Copies of the native test executable act as real versioned tools on PATH.
// The version lives beside each copy so updating a fixture needs no compiler.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		version, err := os.ReadFile(executable + ".version")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(version))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeTool(t *testing.T, dir, name, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, testBinaryName(name))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		// Prefer a hard link to avoid copying a test binary for every tool.
		if err := os.Link(executable, path); err != nil {
			data, err := os.ReadFile(executable)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".version", []byte(version), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
