package config

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/sethvargo/go-envconfig"
)

type CoreConfig struct {
	CookieSecret            string `env:"COOKIE_SECRET, default=00000000000000000000000000000000"`
	DbPath                  string `env:"DB_PATH, default=appview.db"`
	ListenAddr              string `env:"LISTEN_ADDR, default=0.0.0.0:3000"`
	AppviewHost             string `env:"APPVIEW_HOST, default=https://tangled.org"`
	AppviewName             string `env:"APPVIEW_Name, default=Tangled"`
	Dev                     bool   `env:"DEV, default=false"`
	DisallowedNicknamesFile string `env:"DISALLOWED_NICKNAMES_FILE"`

	// temporarily, to add users to default knot and spindle
	AppPassword string `env:"APP_PASSWORD"`

	// uhhhh this is because knot1 is under icy's did
	TmpAltAppPassword string `env:"ALT_APP_PASSWORD"`
}

type OAuthConfig struct {
	ClientSecret string `env:"CLIENT_SECRET"`
	ClientKid    string `env:"CLIENT_KID"`
}

type PlcConfig struct {
	PLCURL string `env:"URL, default=https://plc.directory"`
}

type JetstreamConfig struct {
	Endpoint string `env:"ENDPOINT, default=wss://jetstream1.us-east.bsky.network/subscribe"`
}

type ConsumerConfig struct {
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

type PdsConfig struct {
	Host        string `env:"HOST, default=https://tngl.sh"`
	AdminSecret string `env:"ADMIN_SECRET"`
}

type Cloudflare struct {
	ApiToken           string `env:"API_TOKEN"`
	ZoneId             string `env:"ZONE_ID"`
	TurnstileSiteKey   string `env:"TURNSTILE_SITE_KEY"`
	TurnstileSecretKey string `env:"TURNSTILE_SECRET_KEY"`
}

type LabelConfig struct {
	DefaultLabelDefs []string `env:"DEFAULTS, default=at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/wontfix,at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/good-first-issue,at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/duplicate,at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/documentation,at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/assignee"` // delimiter=,
	GoodFirstIssue   string   `env:"GFI, default=at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/good-first-issue"`
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
	Core          CoreConfig      `env:",prefix=TANGLED_"`
	Jetstream     JetstreamConfig `env:",prefix=TANGLED_JETSTREAM_"`
	Knotstream    ConsumerConfig  `env:",prefix=TANGLED_KNOTSTREAM_"`
	Spindlestream ConsumerConfig  `env:",prefix=TANGLED_SPINDLESTREAM_"`
	Resend        ResendConfig    `env:",prefix=TANGLED_RESEND_"`
	Posthog       PosthogConfig   `env:",prefix=TANGLED_POSTHOG_"`
	Camo          CamoConfig      `env:",prefix=TANGLED_CAMO_"`
	Avatar        AvatarConfig    `env:",prefix=TANGLED_AVATAR_"`
	OAuth         OAuthConfig     `env:",prefix=TANGLED_OAUTH_"`
	Redis         RedisConfig     `env:",prefix=TANGLED_REDIS_"`
	Plc           PlcConfig       `env:",prefix=TANGLED_PLC_"`
	Pds           PdsConfig       `env:",prefix=TANGLED_PDS_"`
	Cloudflare    Cloudflare      `env:",prefix=TANGLED_CLOUDFLARE_"`
	Label         LabelConfig     `env:",prefix=TANGLED_LABEL_"`
}

func LoadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
