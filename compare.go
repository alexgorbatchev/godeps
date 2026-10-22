package godeps

import (
	"fmt"
	"regexp"
	"strings"

	version "github.com/hashicorp/go-version"
)

var (
	rxYtDlp  = regexp.MustCompile(`\b(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?)\b`)
	rxFFmpeg = regexp.MustCompile(`(?:ffmpeg)\s+version\s+(?:n)?([^\s]+)`)
)

// DefaultVersionParser extracts a clean version string from raw tool output banners.
func DefaultVersionParser(name, output string) string {
	line := strings.TrimSpace(output)
	if idx := strings.Index(line, "\n"); idx != -1 {
		line = strings.TrimSpace(line[:idx])
	}

	switch strings.ToLower(name) {
	case "yt-dlp":
		if match := rxYtDlp.FindStringSubmatch(line); len(match) > 1 {
			return match[1]
		}
		return line
	case "ffmpeg":
		if match := rxFFmpeg.FindStringSubmatch(line); len(match) > 1 {
			return match[1]
		}
		return line
	default:
		line = strings.TrimPrefix(line, name)
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "version")
		line = strings.TrimSpace(line)
		if line == "dev" || strings.HasPrefix(line, "v") {
			return strings.TrimPrefix(line, "v")
		}
		return line
	}
}

// CompareVersions checks if actual meets or exceeds the minimum version requirement min.
// Returns nil if satisfied, or an error if actual is below min, empty, or not a
// recognizable version: a check that cannot read a version never passes.
func CompareVersions(actual, min string) error {
	actual = strings.TrimSpace(actual)
	min = strings.TrimSpace(min)

	// Development builds or git snapshots satisfy requirements
	if actual == "dev" || strings.HasPrefix(actual, "N-") || strings.HasPrefix(actual, "git-") || actual == "master" || actual == "main" {
		return nil
	}

	if actual == "" {
		return fmt.Errorf("no version to compare with required minimum %s", min)
	}

	// Use hashicorp/go-version for robust SemVer / CalVer / Segmented comparison
	actVer, errAct := version.NewVersion(actual)
	minVer, errMin := version.NewVersion(min)

	if errAct != nil || errMin != nil {
		// Fallback to manual numeric segment comparison if go-version encounters non-standard formats
		return fallbackCompareSegments(actual, min)
	}

	if actVer.LessThan(minVer) {
		return fmt.Errorf("version %s is below required minimum %s", actual, min)
	}

	return nil
}

func fallbackCompareSegments(actStr, minStr string) error {
	actParts := parseVersionDigits(actStr)
	minParts := parseVersionDigits(minStr)

	if len(actParts) == 0 {
		return fmt.Errorf("version %q cannot be compared with required minimum %s", actStr, minStr)
	}

	maxLen := len(actParts)
	if len(minParts) > maxLen {
		maxLen = len(minParts)
	}

	for i := 0; i < maxLen; i++ {
		actVal := 0
		if i < len(actParts) {
			actVal = actParts[i]
		}
		minVal := 0
		if i < len(minParts) {
			minVal = minParts[i]
		}

		if actVal > minVal {
			return nil
		}
		if actVal < minVal {
			return fmt.Errorf("version %s is below required minimum %s", actStr, minStr)
		}
	}

	return nil
}

func parseVersionDigits(v string) []int {
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}

	rawParts := strings.Split(v, ".")
	var parts []int
	for _, p := range rawParts {
		var val int
		if _, err := fmt.Sscanf(p, "%d", &val); err == nil {
			parts = append(parts, val)
		}
	}
	return parts
}
