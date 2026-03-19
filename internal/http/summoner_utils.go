package http

import (
	"strings"
)

// extractIdentityName extracts the Name field from IDENTITY.md content.
// Matches format: - **Name:** value
func extractIdentityName(content string) string {
	if content == "" {
		return ""
	}
	m := identityNameRe.FindStringSubmatch(content)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// suffixString returns the last n runes of s.
func suffixString(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

// truncateUTF8 truncates s to at most maxLen runes, appending "…" if truncated.
func truncateUTF8(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}

// parseFileResponse extracts file contents and frontmatter from XML-tagged LLM output.
// Frontmatter is stored under the special key "__frontmatter__".
func parseFileResponse(content string) map[string]string {
	files := make(map[string]string)
	matches := fileTagRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		body := strings.TrimSpace(m[2])
		if name != "" && body != "" {
			files[name] = body
		}
	}
	// Extract frontmatter tag if present
	if fm := frontmatterTagRe.FindStringSubmatch(content); len(fm) > 1 {
		if trimmed := strings.TrimSpace(fm[1]); trimmed != "" {
			files[frontmatterKey] = trimmed
		}
	}
	return files
}
