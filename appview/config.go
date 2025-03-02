package appview

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	CookieSecret string `env:"TANGLED_COOKIE_SECRET, default=00000000000000000000000000000000"`
	Hostname     string `env:"TANGLED_HOSTNAME, default=0.0.0.0"`
	Port         string `env:"TANGLED_PORT, default=3000"`
	DbPath       string `env:"TANGLED_DB_PATH, default=appview.db"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
