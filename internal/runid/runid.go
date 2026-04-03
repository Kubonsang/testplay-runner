// Package runid provides run-ID format validation shared across packages.
// Run IDs are lexicographically sortable timestamps: YYYYMMDD-HHMMSS-xxxxxxxx
// where the 8-char hex suffix is crypto-random.
package runid

import "regexp"

// Pattern matches both legacy (YYYYMMDD-HHMMSS) and current (YYYYMMDD-HHMMSS-xxxxxxxx) formats.
var Pattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9a-f]{8})?$`)

// IsValid reports whether id matches the run-ID format.
func IsValid(id string) bool {
	return Pattern.MatchString(id)
}
