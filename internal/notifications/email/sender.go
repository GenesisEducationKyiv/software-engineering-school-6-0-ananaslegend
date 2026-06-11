package email

import (
	"context"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

// Sender renders and delivers a notification over email.
type Sender interface {
	SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error
	SendRelease(ctx context.Context, p contract.SendReleaseRequest) error
}
