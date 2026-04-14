package slug

import (
	"regexp"
	"strings"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// FromTitle derives a URL/branch-safe slug from a task title.
// Lowercase, alphanumeric + hyphens only, max 40 characters.
func FromTitle(title string) string {
	s := strings.ToLower(title)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.TrimRight(s, "-")
	}
	return s
}
