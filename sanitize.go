package godeps

import (
	"strings"
)

// SanitizeStderr cleans raw stderr output from external CLI tools by stripping
// multiline stack traces (Python tracebacks, Go panics, Node traces, etc.)
// and extracting the actual error description.
func SanitizeStderr(stderr string) string {
	raw := strings.TrimSpace(stderr)
	if raw == "" {
		return ""
	}

	// 1. Check for Python Traceback:
	if strings.Contains(raw, "Traceback (most recent call last):") {
		lines := strings.Split(raw, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && !strings.HasPrefix(line, "File ") && !strings.HasPrefix(line, "Traceback") {
				return line
			}
		}
	}

	// 2. Check for tool ERROR: prefix in any line
	if idx := strings.Index(raw, "ERROR:"); idx != -1 {
		errPart := raw[idx:]
		if endIdx := strings.Index(errPart, "\n"); endIdx != -1 {
			return strings.TrimSpace(errPart[:endIdx])
		}
		return strings.TrimSpace(errPart)
	}

	// 3. Check for Go panic:
	if strings.Contains(raw, "panic:") {
		lines := strings.Split(raw, "\n")
		for _, line := range lines {
			line := strings.TrimSpace(line)
			if strings.HasPrefix(line, "panic:") {
				return line
			}
		}
	}

	// 4. Check for Node/JS stack frames (e.g. "Error: ...\n    at ...")
	if strings.Contains(raw, "\n    at ") || strings.Contains(raw, "\n\tat ") {
		lines := strings.Split(raw, "\n")
		var nonStackLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "at ") && trimmed != "" {
				nonStackLines = append(nonStackLines, trimmed)
			}
		}
		if len(nonStackLines) > 0 {
			return strings.Join(nonStackLines, " ")
		}
	}

	// 5. Clean trailing/leading spaces from single-line or multi-line output
	lines := strings.Split(raw, "\n")
	var cleanLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	if len(cleanLines) == 1 {
		return cleanLines[0]
	}
	if len(cleanLines) > 1 {
		return cleanLines[len(cleanLines)-1]
	}

	return raw
}
