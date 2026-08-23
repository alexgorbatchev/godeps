package godeps

import (
	"testing"
)

func TestSanitizeStderr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "clean single-line message",
			input: "ffmpeg not found in PATH",
			want:  "ffmpeg not found in PATH",
		},
		{
			name: "python traceback",
			input: `Traceback (most recent call last):
  File "/Applications/Xcode.app/Contents/Developer/Library/Frameworks/Python3.framework/Versions/3.9/lib/python3.9/runpy.py", line 197, in _run_module_as_main
    return _run_code(code, main_globals, None,
  File "/Applications/Xcode.app/Contents/Developer/Library/Frameworks/Python3.framework/Versions/3.9/lib/python3.9/runpy.py", line 87, in _run_code
    exec(code, run_globals)
  File "/Users/alex/.local/share/fetch-mix/bin/yt-dlp/__main__.py", line 14, in <module>
  File "<frozen zipimport>", line 259, in load_module
  File "/Users/alex/.local/share/fetch-mix/bin/yt-dlp/yt_dlp/__init__.py", line 4, in <module>
ImportError: You are using an unsupported version of Python. Only Python versions 3.10 and above are supported by yt-dlp`,
			want: "ImportError: You are using an unsupported version of Python. Only Python versions 3.10 and above are supported by yt-dlp",
		},
		{
			name: "yt-dlp ERROR line",
			input: `[youtube] Extracting URL: https://www.youtube.com/watch?v=123
[youtube] 123: Downloading webpage
ERROR: [youtube] 123: Private video. Sign in if you've been granted access to this video`,
			want: "ERROR: [youtube] 123: Private video. Sign in if you've been granted access to this video",
		},
		{
			name: "go panic",
			input: `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x104b2c8c8]

goroutine 1 [running]:
main.main()
	/path/to/main.go:42 +0x24`,
			want: "panic: runtime error: invalid memory address or nil pointer dereference",
		},
		{
			name: "node stack trace",
			input: `Error: Cannot find module 'some-package'
    at Function.Module._resolveFilename (node:internal/modules/cjs/loader:1077:15)
    at Function.Module._load (node:internal/modules/cjs/loader:928:27)
    at Function.executeUserEntryPoint [as runMain] (node:internal/modules/run_main:81:12)`,
			want: "Error: Cannot find module 'some-package'",
		},
		{
			name: "node stack trace with only at lines",
			input: `
    at Function.Module._resolveFilename (node:internal/modules/cjs/loader:1077:15)
    at Function.Module._load (node:internal/modules/cjs/loader:928:27)`,
			want: "at Function.Module._load (node:internal/modules/cjs/loader:928:27)",
		},
		{
			name: "multiline plain text",
			input: `first line
second line
final error occurred`,
			want: "final error occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeStderr(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeStderr() = %q, want %q", got, tt.want)
			}
		})
	}
}
