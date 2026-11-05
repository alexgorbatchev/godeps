//go:build unix

package godeps

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckRecipe(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := os.ReadFile("justfile")
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{"tidy", "build-host", "build-windows", "vet-host", "vet-windows", "test"}
	type checkCase struct {
		name       string
		fail       string
		source     string
		uncovered  bool
		wantFailed bool
		wantSteps  int
	}
	tests := []checkCase{
		{name: "complete", wantSteps: len(steps)},
		{name: "unformatted source", source: "package fixture\nfunc ready( )bool{return true}\n", wantFailed: true},
		{name: "invalid source", source: "package fixture\nfunc ready(\n", wantFailed: true},
		{name: "coverage below threshold", uncovered: true, wantFailed: true, wantSteps: len(steps)},
	}
	for i, step := range steps {
		tests = append(tests, checkCase{name: "failed " + step, fail: step, wantFailed: true, wantSteps: i + 1})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			source := tt.source
			if source == "" {
				source = "package fixture\n\nfunc ready() bool { return true }\n"
			}
			files := map[string][]byte{
				"justfile":   recipe,
				"fixture.go": []byte(source),
				"go.mod":     []byte("module fixture\n\ngo 1.26.2\n"),
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// The real justfile and formatter run against isolated source. A
			// controlled Go child lets each later check fail independently.
			tool := "#!/bin/sh\nset -eu\n" +
				"case \"$1\" in\n" +
				"mod) step=tidy ;;\n" +
				"build|vet) step=\"$1-${GOOS:-host}\" ;;\n" +
				"test) step=test ;;\n" +
				"*) exit 42 ;;\nesac\n" +
				"printf '%s\\n' \"$step\" >> \"$GODEPS_CHECK_LOG\"\n" +
				"if [ \"$step\" = \"$GODEPS_CHECK_FAIL\" ]; then exit 23; fi\n" +
				"if [ \"$step\" = test ]; then\n" +
				"printf 'mode: atomic\\nfixture.go:3.1,3.35 1 %s\\n' \"$GODEPS_CHECK_HITS\" > coverage.out\nfi\n"
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(tool), 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "steps.log")
			hits := "1"
			if tt.uncovered {
				hits = "0"
			}
			cmd := CommandContext(t.Context(), just, "--justfile", filepath.Join(dir, "justfile"), "check")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GOOS=", "GODEPS_CHECK_LOG="+log, "GODEPS_CHECK_FAIL="+tt.fail, "GODEPS_CHECK_HITS="+hits)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tt.wantFailed {
				t.Errorf("just check error = %v, want failed=%v\n%s", err, tt.wantFailed, out)
			}
			data, err := os.ReadFile(log)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got, want := strings.Fields(string(data)), steps[:tt.wantSteps]; !slices.Equal(got, want) {
				t.Errorf("executed checks = %v, want %v\n%s", got, want, out)
			}
			for name, want := range files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("just check modified %s", name)
				}
			}
		})
	}
}
