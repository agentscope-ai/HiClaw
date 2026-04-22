package agentconfig

import (
	"strings"
)

// MergeBuiltinSection merges a builtin source into a target markdown document.
// It preserves user content after the builtin-end marker.
//
// Behavior:
//   - If target is empty: returns marker-wrapped source
//   - If target has markers: replaces builtin section, preserves user content
//   - If target has no markers: overwrites with marker-wrapped source
func MergeBuiltinSection(target, source string) string {
	if target == "" {
		return wrapWithMarkers(source, "")
	}

	if strings.Contains(target, BuiltinStart) {
		userContent := extractUserContent(target)
		return wrapWithMarkers(source, userContent)
	}

	// Legacy file without markers — wrap source with markers, preserve target as user content
	return wrapWithMarkers(source, target)
}

// ExtractFrontmatter separates YAML frontmatter from the body.
// Returns (frontmatter, body). If no frontmatter, frontmatter is empty.
func ExtractFrontmatter(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			fm := strings.Join(lines[:i+1], "\n")
			body := ""
			if i+1 < len(lines) {
				body = strings.Join(lines[i+1:], "\n")
			}
			return fm, strings.TrimLeft(body, "\n")
		}
	}

	return "", content
}

func wrapWithMarkers(source, userContent string) string {
	_, body := ExtractFrontmatter(source)

	var b strings.Builder
	b.WriteString(BuiltinHeader)
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n")
	b.WriteString(BuiltinEnd)
	b.WriteString("\n")

	if userContent != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(userContent, "\n"))
		b.WriteString("\n")
	}

	return b.String()
}

func extractUserContent(target string) string {
	// Use LastIndex because BuiltinHeader references the end marker in backticks
	idx := strings.LastIndex(target, BuiltinEnd)
	if idx < 0 {
		return ""
	}
	after := target[idx+len(BuiltinEnd):]
	after = strings.TrimLeft(after, "\n")
	if strings.TrimSpace(after) == "" {
		return ""
	}
	return after
}
