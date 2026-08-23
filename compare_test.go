package godeps

import (
	"testing"
)

func TestDefaultVersionParser(t *testing.T) {
	tests := []struct {
		name   string
		dep    string
		output string
		want   string
	}{
		{"yt-dlp calver", "yt-dlp", "2024.08.01\n", "2024.08.01"},
		{"ffmpeg full banner", "ffmpeg", "ffmpeg version 6.0-extra Copyright (c) 2000-2023", "6.0-extra"},
		{"fetch-track dev", "fetch-track", "fetch-track version dev\n", "dev"},
		{"fetch-track semver v-prefixed", "fetch-track", "fetch-track version v1.4.0\n", "1.4.0"},
		{"generic version", "custom-tool", "custom-tool version 2.1.0", "2.1.0"},
		{"empty output", "yt-dlp", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultVersionParser(tt.dep, tt.output)
			if got != tt.want {
				t.Errorf("DefaultVersionParser(%q, %q) = %q, want %q", tt.dep, tt.output, got, tt.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		actual  string
		min     string
		wantErr bool
	}{
		{"equal semver", "1.4.0", "1.4.0", false},
		{"higher semver", "1.4.1", "1.4.0", false},
		{"lower semver", "1.3.9", "1.4.0", true},
		{"calver equal", "2024.08.01", "2024.08.01", false},
		{"calver newer", "2026.08.19", "2024.08.01", false},
		{"calver older", "2023.01.01", "2024.08.01", true},
		{"dev build satisfies", "dev", "1.4.0", false},
		{"git snapshot satisfies", "git-master", "1.4.0", false},
		{"ffmpeg N-build satisfies", "N-12345", "4.4", false},
		{"v prefix stripped", "v1.5.0", "1.4.0", false},
		{"empty actual", "", "1.0.0", false},
		{"fallback invalid non-semver higher", "invalid.2.3", "invalid.1.3", false},
		{"fallback invalid non-semver lower", "invalid.1.3", "invalid.2.3", true},
		{"fallback empty segments", "invalid-no-digits", "1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.actual, tt.min)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareVersions(%q, %q) error = %v, wantErr %v", tt.actual, tt.min, err, tt.wantErr)
			}
		})
	}
}
