package domain

import (
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestGenerateToken_LengthAndCharset(t *testing.T) {
	tok, err := GenerateToken()
	require.NoError(t, err)
	assert.True(t, tokenRe.MatchString(tok),
		"token %q must be 43 chars of base64url alphabet", tok)
}

func TestGenerateToken_UniqueAcross10k(t *testing.T) {
	const n = 10_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		tok, err := GenerateToken()
		require.NoError(t, err)
		_, dup := seen[tok]
		require.False(t, dup, "collision at iteration %d: %q", i, tok)
		seen[tok] = struct{}{}
	}
}

func TestGenerateToken_ParallelSafety(t *testing.T) {
	const workers = 50
	const perWorker = 200
	var mu sync.Mutex
	seen := make(map[string]struct{}, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				tok, err := GenerateToken()
				require.NoError(t, err)
				mu.Lock()
				_, dup := seen[tok]
				seen[tok] = struct{}{}
				mu.Unlock()
				require.False(t, dup, "concurrent collision: %q", tok)
			}
		}()
	}
	wg.Wait()
}
