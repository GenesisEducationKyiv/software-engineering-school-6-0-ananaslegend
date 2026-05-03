package app

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	"github.com/ananaslegend/reposeetory/pkg/transactor"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/confirmer"
	confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
	"github.com/ananaslegend/reposeetory/internal/notifier"
	notifierrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
)

func runWorkers(
	ctx context.Context,
	wg *sync.WaitGroup,
	cfg config.Config,
	txr transactor.Transactor,
	pool *pgxpool.Pool,
	mail emailer.Emailer,
	releases scanner.ReleaseProvider,
	reg *prometheus.Registry,
) {
	scan := scanner.New(scanner.Config{
		Tx:       txr,
		Repo:     scannerrepo.New(pool),
		GitHub:   releases,
		Interval: cfg.ScannerInterval,
		Registry: reg,
	})
	wg.Go(func() { ; scan.Run(ctx) })

	notify := notifier.New(notifier.Config{
		Tx:       txr,
		Repo:     notifierrepo.New(pool),
		Mailer:   mail,
		Interval: cfg.NotifierInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})
	wg.Go(func() { ; notify.Run(ctx) })

	confirm := confirmer.New(confirmer.Config{
		Tx:       txr,
		Repo:     confirmerrepo.New(pool),
		Mailer:   mail,
		Interval: cfg.ConfirmerInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})
	wg.Go(func() { ; confirm.Run(ctx) })
}
