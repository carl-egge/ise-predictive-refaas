package domain

import (
	"strings"
	"unicode"
)

// MinimizeString removes control characters from s and returns a compact,
// human-readable string useful for comparing outputs.
func MinimizeString(s string) string {
	var builder strings.Builder
	for _, r := range s {
		if !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
