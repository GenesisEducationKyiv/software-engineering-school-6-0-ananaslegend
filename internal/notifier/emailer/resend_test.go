package emailer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/resend/resend-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// mockedResendMailer spins up a fake Resend endpoint at srv and returns a
// ResendMailer whose internal client posts there. Re-uses the SDK's
// NewCustomClient + exported BaseURL — no monkey-patching of globals.
func mockedResendMailer(t *testing.T, srv *httptest.Server, from string) *ResendMailer {
	t.Helper()
	c := resend.NewCustomClient(srv.Client(), "test-key")
	parsed, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	c.BaseURL = parsed
	return NewResendMailerWithClient(c, from)
}

func TestResend_SendConfirmation_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/emails", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
	}))
	t.Cleanup(srv.Close)

	m := mockedResendMailer(t, srv, "noreply@app.test")
	require.NoError(t, m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
		To:           "u@example.com",
		ConfirmURL:   "https://app.test/confirm/abc",
		RepoFullName: "golang/go",
	}))

	assert.Equal(t, "noreply@app.test", captured["from"])
	assert.Equal(t, []any{"u@example.com"}, captured["to"])
	subj, _ := captured["subject"].(string)
	assert.Contains(t, subj, "golang/go")
	assert.Contains(t, subj, "Confirm")
	html, _ := captured["html"].(string)
	assert.Contains(t, html, "https://app.test/confirm/abc")
	text, _ := captured["text"].(string)
	assert.NotEmpty(t, text, "plain-text fallback must be set")
}

func TestResend_SendRelease_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
	}))
	t.Cleanup(srv.Close)

	m := mockedResendMailer(t, srv, "noreply@app.test")
	require.NoError(t, m.SendRelease(context.Background(), domain.SendReleaseParams{
		To:             "u@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "v1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/v1.22.0",
		UnsubscribeURL: "https://app.test/unsubscribe/xyz",
	}))

	subj, _ := captured["subject"].(string)
	assert.Contains(t, subj, "v1.22.0")
	assert.Contains(t, subj, "golang/go")
	html, _ := captured["html"].(string)
	assert.Contains(t, html, "https://github.com/golang/go/releases/tag/v1.22.0")
	assert.Contains(t, html, "https://app.test/unsubscribe/xyz")
}

func TestResend_SendConfirmation_PropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"bad from"}`))
	}))
	t.Cleanup(srv.Close)

	m := mockedResendMailer(t, srv, "bogus")
	err := m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
		To: "u@example.com", ConfirmURL: "x", RepoFullName: "a/b",
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "send confirmation email"),
		"expected wrapped error from SendConfirmation, got %q", err)
}
