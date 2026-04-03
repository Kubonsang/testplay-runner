//go:build windows

package unity

import "strings"

// envKeysEqual reports whether two environment variable keys are equal.
// On Windows, env keys are case-insensitive.
func envKeysEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}
