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
	PlcUrl            string  `env:"PLC_URL, default=plc.directory"`
	Dev               bool    `env:"DEV, default=false"`
	Owner             string  `env:"OWNER, required"`
	Secrets           Secrets `env:",prefix=SECRETS_"`
	LogDir            string  `env:"LOG_DIR, default=/var/log/spindle"`
	QueueSize         int     `env:"QUEUE_SIZE, default=100"`
	MaxJobCount       int     `env:"MAX_JOB_COUNT, default=2"` // max number of jobs that run at a time
}

func (s Server) Did() syntax.DID {
	return syntax.DID(fmt.Sprintf("did:web:%s", s.Hostname))
}

type Secrets struct {
	Provider string        `env:"PROVIDER, default=sqlite"`
	OpenBao  OpenBaoConfig `env:",prefix=OPENBAO_"`
}

type OpenBaoConfig struct {
	ProxyAddr string `env:"PROXY_ADDR, default=http://127.0.0.1:8200"`
	Mount     string `env:"MOUNT, default=spindle"`
}

type NixeryPipelines struct {
	Nixery          string `env:"NIXERY, default=nixery.tangled.sh"`
	WorkflowTimeout string `env:"WORKFLOW_TIMEOUT, default=5m"`
}

type Config struct {
	Server          Server          `env:",prefix=SPINDLE_SERVER_"`
	NixeryPipelines NixeryPipelines `env:",prefix=SPINDLE_NIXERY_PIPELINES_"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
