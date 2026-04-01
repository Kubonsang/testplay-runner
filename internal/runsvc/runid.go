package runsvc

import (
	"crypto/rand"
	"fmt"
	"time"
)

// generateRunID returns a collision-resistant run identifier.
// Format: "20060102-150405-xxxxxxxx" where the suffix is 4 random bytes in hex.
// Using crypto/rand (not math/rand) ensures uniqueness even under parallel runs
// started within the same second.
func generateRunID(t time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", t.Format("20060102-150405"), b)
}
