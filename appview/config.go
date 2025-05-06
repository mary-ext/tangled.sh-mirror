package appview

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	CookieSecret       string `env:"TANGLED_COOKIE_SECRET, default=00000000000000000000000000000000"`
	DbPath             string `env:"TANGLED_DB_PATH, default=appview.db"`
	ListenAddr         string `env:"TANGLED_LISTEN_ADDR, default=0.0.0.0:3000"`
	Dev                bool   `env:"TANGLED_DEV, default=false"`
	JetstreamEndpoint  string `env:"TANGLED_JETSTREAM_ENDPOINT, default=wss://jetstream1.us-east.bsky.network/subscribe"`
	ResendApiKey       string `env:"TANGLED_RESEND_API_KEY"`
	CamoHost           string `env:"TANGLED_CAMO_HOST, default=https://camo.tangled.sh"`
	CamoSharedSecret   string `env:"TANGLED_CAMO_SHARED_SECRET"`
	AvatarSharedSecret string `env:"TANGLED_AVATAR_SHARED_SECRET"`
	AvatarHost         string `env:"TANGLED_AVATAR_HOST, default=https://avatar.tangled.sh"`
	EnableTelemetry    bool   `env:"TANGLED_TELEMETRY_ENABLED, default=false"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
