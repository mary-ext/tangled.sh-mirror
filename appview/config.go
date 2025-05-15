package appview

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type CoreConfig struct {
	CookieSecret string `env:"COOKIE_SECRET, default=00000000000000000000000000000000"`
	DbPath       string `env:"DB_PATH, default=appview.db"`
	ListenAddr   string `env:"LISTEN_ADDR, default=0.0.0.0:3000"`
	AppviewHost  string `env:"APPVIEW_HOST, default=https://tangled.sh"`
	Dev          bool   `env:"DEV, default=false"`
}

type OAuthConfig struct {
	Jwks string `env:"JWKS"`
}

type JetstreamConfig struct {
	Endpoint string `env:"ENDPOINT, default=wss://jetstream1.us-east.bsky.network/subscribe"`
}

type ResendConfig struct {
	ApiKey string `env:"API_KEY"`
}

type CamoConfig struct {
	Host         string `env:"HOST, default=https://camo.tangled.sh"`
	SharedSecret string `env:"SHARED_SECRET"`
}

type AvatarConfig struct {
	Host         string `env:"HOST, default=https://avatar.tangled.sh"`
	SharedSecret string `env:"SHARED_SECRET"`
}

type PosthogConfig struct {
	ApiKey   string `env:"API_KEY"`
	Endpoint string `env:"ENDPOINT, default=https://eu.i.posthog.com"`
}

type Config struct {
	Core      CoreConfig      `env:",prefix=TANGLED_"`
	Jetstream JetstreamConfig `env:",prefix=TANGLED_JETSTREAM_"`
	Resend    ResendConfig    `env:",prefix=TANGLED_RESEND_"`
	Posthog   PosthogConfig   `env:",prefix=TANGLED_POSTHOG_"`
	Camo      CamoConfig      `env:",prefix=TANGLED_CAMO_"`
	Avatar    AvatarConfig    `env:",prefix=TANGLED_AVATAR_"`
	OAuth     OAuthConfig     `env:",prefix=TANGLED_OAUTH_"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
