package emailer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func TestConfirmationHTML_ContainsURLAndRepo(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, confirmationHTMLTmpl.Execute(&buf, domain.SendConfirmationParams{
		To:           "u@example.com",
		ConfirmURL:   "https://app.test/confirm/abc",
		RepoFullName: "golang/go",
	}))
	body := buf.String()
	assert.Contains(t, body, "https://app.test/confirm/abc")
	assert.Contains(t, body, "golang/go")
}

func TestReleaseHTML_ContainsTagAndReleaseURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, releaseHTMLTmpl.Execute(&buf, domain.SendReleaseParams{
		To:             "u@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "v1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/v1.22.0",
		UnsubscribeURL: "https://app.test/unsubscribe/xyz",
	}))
	body := buf.String()
	assert.Contains(t, body, "v1.22.0")
	assert.Contains(t, body, "https://github.com/golang/go/releases/tag/v1.22.0")
	assert.Contains(t, body, "https://app.test/unsubscribe/xyz")
}

func TestReleaseHTML_EscapesHTMLInRepoName(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, releaseHTMLTmpl.Execute(&buf, domain.SendReleaseParams{
		RepoFullName: "<script>alert(1)</script>",
		ReleaseTag:   "v1",
		ReleaseURL:   "https://example.test",
	}))
	body := buf.String()
	assert.NotContains(t, body, "<script>alert(1)</script>",
		"raw script tag must be escaped by html/template")
	assert.True(t, strings.Contains(body, "&lt;script&gt;"),
		"expected HTML-escaped repo name in %q", body)
}

func TestConfirmationTXT_ContainsConfirmURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, confirmationTXTTmpl.Execute(&buf, domain.SendConfirmationParams{
		ConfirmURL:   "https://app.test/confirm/x",
		RepoFullName: "a/b",
	}))
	assert.Contains(t, buf.String(), "https://app.test/confirm/x")
}
