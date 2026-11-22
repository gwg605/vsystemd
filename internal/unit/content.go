package unit

import (
	"crypto/sha256"
	"encoding/hex"
)

// Content is the raw content of a unit file (the file itself or its applied
// snapshot).
type Content []byte

// Hash returns the lowercase hex SHA-256 of the content.
func (c Content) Hash() string {
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:])
}
