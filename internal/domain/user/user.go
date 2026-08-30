// Package user is the per-player isolation unit: one browser sign-in,
// its EVE characters and their tokens. The EVE application belongs to the
// process, not to the user.
package user

import (
	"crypto/rand"
	"encoding/hex"
)

// User is one player of this instance.
type User struct {
	ID        string
	CreatedAt string
	Dir       string
}

// NewID returns a random 16-hex-char user id.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
