package config

import (
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	HTTPAddr            string        `envconfig:"HTTP_ADDR" default:":8080"`
	HTTPReadTimeout     time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"10s"`
	HTTPWriteTimeout    time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"10s"`
	HTTPShutdownTimeout time.Duration `envconfig:"HTTP_SHUTDOWN_TIMEOUT" default:"15s"`

	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`
	DBMaxConns  int32  `envconfig:"DB_MAX_CONNS" default:"10"`

	AppBaseURL      string        `envconfig:"APP_BASE_URL" default:"http://localhost:8080"`
	ConfirmTokenTTL time.Duration `envconfig:"CONFIRM_TOKEN_TTL" default:"24h"`

	LogLevel  string `envconfig:"LOG_LEVEL" default:"info"`
	LogPretty bool   `envconfig:"LOG_PRETTY" default:"true"`

	SMTPHost      string `envconfig:"SMTP_HOST"`
	SMTPPort      int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser      string `envconfig:"SMTP_USER"`
	SMTPPass      string `envconfig:"SMTP_PASS"`
	SMTPFrom      string `envconfig:"SMTP_FROM"`
	SMTPTLSPolicy string `envconfig:"SMTP_TLS_POLICY" default:"starttls"`

	ResendAPIKey string `envconfig:"RESEND_API_KEY"`
	ResendFrom   string `envconfig:"RESEND_FROM"`

	GitHubToken string `envconfig:"GITHUB_TOKEN"`

	ScannerInterval  time.Duration `envconfig:"SCANNER_INTERVAL" default:"5m"`
	NotifierInterval time.Duration `envconfig:"NOTIFIER_INTERVAL" default:"30s"`
}

func Load() (Config, error) {
	// ignore error — .env may not exist in production (env vars come from process)
	_ = godotenv.Load()

	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
