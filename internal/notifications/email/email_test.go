package email_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
	"github.com/ananaslegend/reposeetory/internal/notifications/email"
)

// StubMailer must satisfy Sender and render without touching the network.
func TestStubMailer_SatisfiesSenderAndRenders(t *testing.T) {
	var e email.Sender = email.NewStubMailer()

	require.NoError(t, e.SendRelease(context.Background(), contract.SendReleaseRequest{
		To: "u@example.com", RepoFullName: "owner/name", ReleaseTag: "v1",
		ReleaseURL: "https://x", UnsubscribeURL: "https://y",
	}))
	require.NoError(t, e.SendConfirmation(context.Background(), contract.SendConfirmationRequest{
		To: "u@example.com", ConfirmURL: "https://c", RepoFullName: "owner/name",
	}))
}
