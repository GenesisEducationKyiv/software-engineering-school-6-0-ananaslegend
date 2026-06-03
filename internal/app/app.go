package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/pkg/transactor"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/observability/logshipper"
)

// Run wires up all application components and blocks until ctx is cancelled.
func Run(ctx context.Context) {
	cfg, err := config.Load()
	if err != nil {
		l := zerolog.New(os.Stderr)
		l.Fatal().Err(err).Msg("load config")
	}

	bootstrap := New(LoggerConfig{
		Level:       cfg.LogLevel,
		Pretty:      cfg.LogPretty,
		ServiceName: cfg.LogServiceName,
		Env:         cfg.LogEnv,
		Version:     cfg.LogVersion,
	}, nil)

	if err = runMigrations(cfg.DatabaseURL, bootstrap); err != nil {
		bootstrap.Fatal().Err(err).Msg("run migrations")
	}

	pool, err := newPostgresDatabase(ctx, cfg)
	if err != nil {
		bootstrap.Fatal().Err(err).Msg("connect to database")
	}

	txr := transactor.New(pool)

	rdb, err := NewRedisClient(cfg.RedisURL)
	if err != nil {
		bootstrap.Warn().Err(err).Msg("redis unavailable, github caching disabled")
	}

	metricRegistry := newMetricsRegistry(pool)
	r := newREDs(metricRegistry)

	shipper, shipErr := logshipper.New(logshipper.Config{
		URL:           cfg.VectorIngestURL,
		BufferSize:    cfg.LogShipperBufferSize,
		BatchSize:     cfg.LogShipperBatchSize,
		FlushInterval: cfg.LogShipperFlushInterval,
		Registry:      metricRegistry,
		Logger:        bootstrap,
	})
	if shipErr != nil {
		bootstrap.Warn().Err(shipErr).Msg("log shipper disabled")
		shipper = nil
	}

	// Real logger with shipper attached (if URL configured).
	var shipperWriter io.Writer
	if shipper != nil {
		shipperWriter = shipper
	}
	log := New(LoggerConfig{
		Level:       cfg.LogLevel,
		Pretty:      cfg.LogPretty,
		ServiceName: cfg.LogServiceName,
		Env:         cfg.LogEnv,
		Version:     cfg.LogVersion,
	}, shipperWriter)

	mailSender, err := newEmailer(cfg, log, r.Email)
	if err != nil {
		log.Fatal().Err(err).Msg("create mailer")
	}

	releaseProvider := newReleaseProvider(cfg, log, metricRegistry, rdb, r.GithubClient)

	var cronsWG sync.WaitGroup
	runWorkers(ctx, &cronsWG, cfg, txr, pool, mailSender, releaseProvider, metricRegistry, r)

	srv := newHTTPServer(cfg, pool, log, metricRegistry)

	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer cancel()

	if err = srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}

	cronsWG.Wait()
	rdb.Close() //nolint:errcheck
	pool.Close()

	if shipper != nil {
		shipCtx, shipCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shipper.Close(shipCtx)
		shipCancel()
	}

	log.Info().Msg("shutdown complete")
}
