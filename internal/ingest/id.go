package ingest

import (
	"crypto/rand"
	"encoding/hex"
)

// defaultID returns a random 128-bit hex id. crypto/rand is used so ids are
// collision-resistant without coordinating across worker goroutines.
func defaultID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read on a healthy system does not fail; panic surfaces the
		// catastrophic case rather than silently issuing a degenerate id.
		panic("ingest: entropy source failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
