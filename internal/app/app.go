package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"

	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
)

// Run wires up all application components and blocks until ctx is cancelled.
func Run(ctx context.Context) {
	cfg, err := config.Load()
	if err != nil {
		l := zerolog.New(os.Stderr)
		l.Fatal().Err(err).Msg("load config")
	}

	log := New(LoggerConfig{
		Level:  cfg.LogLevel,
		Pretty: cfg.LogPretty,
	})

	if err = runMigrations(cfg.DatabaseURL, log); err != nil {
		log.Fatal().Err(err).Msg("run migrations")
	}

	pool, err := newPostgresDatabase(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}

	txr := transactor.New(pool)

	rdb, err := NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Warn().Err(err).Msg("redis unavailable, github caching disabled")
	}

	metricRegistry := newMetricsRegistry(pool)

	mailSender, err := newEmailer(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("create mailer")
	}

	releaseProvider := newReleaseProvider(cfg, log, metricRegistry, rdb)

	var cronsWG sync.WaitGroup
	runWorkers(ctx, &cronsWG, cfg, txr, pool, mailSender, releaseProvider, metricRegistry)

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

	log.Info().Msg("shutdown complete")
}
