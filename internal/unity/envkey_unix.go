//go:build !windows

package unity

// envKeysEqual reports whether two environment variable keys are equal.
// On Unix, env keys are case-sensitive.
func envKeysEqual(a, b string) bool {
	return a == b
}
