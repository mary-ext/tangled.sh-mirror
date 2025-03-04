package appview

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	CookieSecret string `env:"TANGLED_COOKIE_SECRET, default=00000000000000000000000000000000"`
	DbPath       string `env:"TANGLED_DB_PATH, default=appview.db"`
	ListenAddr   string `env:"TANGLED_LISTEN_ADDR, default=0.0.0.0:3000"`
	Dev          bool   `env:"TANGLED_DEV, default=false"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
