package id

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

var randomReader io.Reader = rand.Reader

// New generates a random 32-character hex ID.
func New() string {
	b := make([]byte, 16)
	if _, err := io.ReadFull(randomReader, b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}
