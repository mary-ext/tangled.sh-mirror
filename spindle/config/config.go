package config

import (
	"context"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/sethvargo/go-envconfig"
)

type Server struct {
	ListenAddr        string  `env:"LISTEN_ADDR, default=0.0.0.0:6555"`
	DBPath            string  `env:"DB_PATH, default=spindle.db"`
	Hostname          string  `env:"HOSTNAME, required"`
	JetstreamEndpoint string  `env:"JETSTREAM_ENDPOINT, default=wss://jetstream1.us-west.bsky.network/subscribe"`
	Dev               bool    `env:"DEV, default=false"`
	Owner             string  `env:"OWNER, required"`
	Secrets           Secrets `env:",prefix=SECRETS_"`
}

func (s Server) Did() syntax.DID {
	return syntax.DID(fmt.Sprintf("did:web:%s", s.Hostname))
}

type Secrets struct {
	Provider string        `env:"PROVIDER, default=sqlite"`
	OpenBao  OpenBaoConfig `env:",prefix=OPENBAO_"`
}

type OpenBaoConfig struct {
	Addr     string `env:"ADDR"`
	RoleID   string `env:"ROLE_ID"`
	SecretID string `env:"SECRET_ID"`
	Mount    string `env:"MOUNT, default=spindle"`
}

type Pipelines struct {
	Nixery          string `env:"NIXERY, default=nixery.tangled.sh"`
	WorkflowTimeout string `env:"WORKFLOW_TIMEOUT, default=5m"`
	LogDir          string `env:"LOG_DIR, default=/var/log/spindle"`
}

type Config struct {
	Server    Server    `env:",prefix=SPINDLE_SERVER_"`
	Pipelines Pipelines `env:",prefix=SPINDLE_PIPELINES_"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
