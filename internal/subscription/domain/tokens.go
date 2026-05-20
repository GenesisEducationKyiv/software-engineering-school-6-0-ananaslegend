package domain

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
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

// ConfirmTokens bundles the two tokens issued at subscription creation
// together with the confirm token's expiration time.
type ConfirmTokens struct {
	Confirm          string
	ConfirmExpiresAt time.Time
	Unsubscribe      string
}

// NewConfirmTokens generates a confirm/unsubscribe pair and computes the confirm expiration.
func NewConfirmTokens(now time.Time, ttl time.Duration) (ConfirmTokens, error) {
	confirm, err := GenerateToken()
	if err != nil {
		return ConfirmTokens{}, fmt.Errorf("domain.NewConfirmTokens: GenerateToken (confirm): %w", err)
	}
	unsubscribe, err := GenerateToken()
	if err != nil {
		return ConfirmTokens{}, fmt.Errorf("domain.NewConfirmTokens: GenerateToken (unsubscribe): %w", err)
	}
	return ConfirmTokens{
		Confirm:          confirm,
		ConfirmExpiresAt: now.Add(ttl),
		Unsubscribe:      unsubscribe,
	}, nil
}
