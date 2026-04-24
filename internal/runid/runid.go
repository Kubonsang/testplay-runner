// Package runid provides run-ID generation and format validation shared
// across packages. Run IDs are lexicographically sortable timestamps:
// YYYYMMDD-HHMMSS-xxxxxxxx where the 8-char hex suffix is crypto-random.
package runid

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"time"
)

// Pattern matches both legacy (YYYYMMDD-HHMMSS) and current (YYYYMMDD-HHMMSS-xxxxxxxx) formats.
var Pattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9a-f]{8})?$`)

// IsValid reports whether id matches the run-ID format.
func IsValid(id string) bool {
	return Pattern.MatchString(id)
}

// Generate returns a collision-resistant run identifier.
// Format: "20060102-150405-xxxxxxxx" where the suffix is 4 random bytes in hex.
// Using crypto/rand (not math/rand) ensures uniqueness even under parallel runs
// started within the same second.
func Generate(t time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", t.Format("20060102-150405"), b)
}
