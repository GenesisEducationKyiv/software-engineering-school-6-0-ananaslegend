package contract_test

import (
	"encoding/json"
	htmltpl "html/template"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

func TestSendReleaseRequest_JSONTags(t *testing.T) {
	in := contract.SendReleaseRequest{
		To:             "user@example.com",
		RepoFullName:   "owner/name",
		ReleaseTag:     "v1.2.3",
		ReleaseURL:     "https://github.com/owner/name/releases/tag/v1.2.3",
		UnsubscribeURL: "https://app.example.com/api/unsubscribe/tok",
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)

	var out contract.SendReleaseRequest
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, out)
	require.Contains(t, string(b), `"repo_full_name"`)
	require.Contains(t, string(b), `"unsubscribe_url"`)
}

// Guards that the template field names email templates rely on
// still exist on the request struct after the move from domain.
func TestSendReleaseRequest_TemplateFieldsResolve(t *testing.T) {
	tmpl := htmltpl.Must(htmltpl.New("t").Parse(
		`{{.RepoFullName}} {{.ReleaseTag}} {{.ReleaseURL}} {{.UnsubscribeURL}}`))
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, contract.SendReleaseRequest{
		RepoFullName: "owner/name", ReleaseTag: "v1", ReleaseURL: "u", UnsubscribeURL: "x",
	}))
	require.Equal(t, "owner/name v1 u x", buf.String())
}

func TestSendConfirmationRequest_TemplateFieldsResolve(t *testing.T) {
	tmpl := htmltpl.Must(htmltpl.New("t").Parse(`{{.RepoFullName}} {{.ConfirmURL}}`))
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, contract.SendConfirmationRequest{
		RepoFullName: "owner/name", ConfirmURL: "c",
	}))
	require.Equal(t, "owner/name c", buf.String())
}
