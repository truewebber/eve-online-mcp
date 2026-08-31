// The EVE application belongs to the process, not to the user.
package user

import (
	"crypto/rand"
	"encoding/hex"
)

type User struct {
	ID        string
	CreatedAt string
}

func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
