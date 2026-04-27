package emailer

import (
	"context"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type Emailer interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
	SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}
