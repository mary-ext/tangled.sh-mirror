package config

import (
	"context"
	"fmt"
	"net/url"
	"time"

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

type KnotstreamConfig struct {
	RetryInterval     time.Duration `env:"RETRY_INTERVAL, default=60s"`
	MaxRetryInterval  time.Duration `env:"MAX_RETRY_INTERVAL, default=120m"`
	ConnectionTimeout time.Duration `env:"CONNECTION_TIMEOUT, default=5s"`
	WorkerCount       int           `env:"WORKER_COUNT, default=64"`
	QueueSize         int           `env:"QUEUE_SIZE, default=100"`
}

type ResendConfig struct {
	ApiKey   string `env:"API_KEY"`
	SentFrom string `env:"SENT_FROM, default=noreply@notifs.tangled.sh"`
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

type RedisConfig struct {
	Addr     string `env:"ADDR, default=localhost:6379"`
	Password string `env:"PASS"`
	DB       int    `env:"DB, default=0"`
}

func (cfg RedisConfig) ToURL() string {
	u := &url.URL{
		Scheme: "redis",
		Host:   cfg.Addr,
		Path:   fmt.Sprintf("/%d", cfg.DB),
	}

	if cfg.Password != "" {
		u.User = url.UserPassword("", cfg.Password)
	}

	return u.String()
}

type Config struct {
	Core       CoreConfig       `env:",prefix=TANGLED_"`
	Jetstream  JetstreamConfig  `env:",prefix=TANGLED_JETSTREAM_"`
	Knotstream KnotstreamConfig `env:",prefix=TANGLED_KNOTSTREAM_"`
	Resend     ResendConfig     `env:",prefix=TANGLED_RESEND_"`
	Posthog    PosthogConfig    `env:",prefix=TANGLED_POSTHOG_"`
	Camo       CamoConfig       `env:",prefix=TANGLED_CAMO_"`
	Avatar     AvatarConfig     `env:",prefix=TANGLED_AVATAR_"`
	OAuth      OAuthConfig      `env:",prefix=TANGLED_OAUTH_"`
	Redis      RedisConfig      `env:",prefix=TANGLED_REDIS_"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
