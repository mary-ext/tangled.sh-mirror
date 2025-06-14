package config

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Server struct {
	ListenAddr        string `env:"LISTEN_ADDR, default=0.0.0.0:6555"`
	DBPath            string `env:"DB_PATH, default=spindle.db"`
	Hostname          string `env:"HOSTNAME, required"`
	JetstreamEndpoint string `env:"JETSTREAM_ENDPOINT, default=wss://jetstream1.us-west.bsky.network/subscribe"`
	Dev               bool   `env:"DEV, default=false"`
	Owner             string `env:"OWNER, required"`
}

type Config struct {
	Server Server   `env:",prefix=SPINDLE_SERVER_"`
	Knots  []string `env:"SPINDLE_SUBSCRIBED_KNOTS,required"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
