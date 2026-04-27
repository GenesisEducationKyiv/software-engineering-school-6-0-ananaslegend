package github

import (
	"context"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

type StubClient struct{}

func NewStubClient() *StubClient {
	return &StubClient{}
}

func (s *StubClient) RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error) {
	zerolog.Ctx(ctx).Debug().
		Str("owner", p.Owner).
		Str("name", p.Name).
		Msg("github stub: repo exists check — returning true")
	return true, nil
}
