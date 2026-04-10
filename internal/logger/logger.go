package logger

import (
	"os"

	"github.com/rs/zerolog"
)

type Config struct {
	Level  string
	Pretty bool
}

func New(cfg Config) zerolog.Logger {
	var l zerolog.Logger
	if cfg.Pretty {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	} else {
		l = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}

	lvl, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	leveledLogger := l.Level(lvl)

	zerolog.DefaultContextLogger = &leveledLogger

	return leveledLogger
}
