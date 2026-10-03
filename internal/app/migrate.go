package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog"

	dbmigrations "github.com/ananaslegend/reposeetory/migrations"
)

func runMigrations(databaseURL string, log zerolog.Logger) error {
	src, err := iofs.New(dbmigrations.FS, ".")
	if err != nil {
		return fmt.Errorf("app.runMigrations: iofs.New: %w", err)
	}

	pgx5URL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(databaseURL, "postgres://"), "postgresql://")

	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL)
	if err != nil {
		return fmt.Errorf("app.runMigrations: migrate.NewWithSourceInstance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("app.runMigrations: Migrate.Up: %w", err)
	}

	log.Info().Msg("migrations applied")
	return nil
}
