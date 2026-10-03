package app

import (
	"io"
	"os"

	"github.com/rs/zerolog"
)

type LoggerConfig struct {
	Level       string
	Pretty      bool
	ServiceName string
	Env         string
	Version     string
}

// New constructs the application logger. If extra is non-nil, every event is also
// written to it via zerolog.MultiLevelWriter (used to attach the log shipper).
func New(cfg LoggerConfig, extra io.Writer) zerolog.Logger {
	var base io.Writer
	if cfg.Pretty {
		base = zerolog.ConsoleWriter{Out: os.Stderr}
	} else {
		base = os.Stderr
	}
	out := base
	if extra != nil {
		out = zerolog.MultiLevelWriter(base, extra)
	}

	l := zerolog.New(out).With().
		Timestamp().
		Str("service", cfg.ServiceName).
		Str("env", cfg.Env).
		Str("version", cfg.Version).
		Logger()

	lvl, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	leveled := l.Level(lvl)
	zerolog.DefaultContextLogger = &leveled
	return leveled
}
