package domain

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateToken returns a 32-byte cryptographically random token encoded as base64url (no padding).
// The resulting string is 43 characters long and safe for use in URLs.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
