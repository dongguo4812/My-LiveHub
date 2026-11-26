package desktop

import "strings"

// firstNonEmpty returns the first non-blank string without altering its value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
