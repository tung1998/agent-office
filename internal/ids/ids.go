// Package ids generates sortable, URL-safe identifiers (ULID).
package ids

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a lowercase ULID, optionally prefixed ("usr" -> "usr_01j...").
func New(prefix string) string {
	mu.Lock()
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
	mu.Unlock()
	s := strings.ToLower(id.String())
	if prefix == "" {
		return s
	}
	return prefix + "_" + s
}
