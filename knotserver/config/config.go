package config

import (
	"context"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/sethvargo/go-envconfig"
)

type Repo struct {
	ScanPath   string   `env:"SCAN_PATH, default=/home/git"`
	Readme     []string `env:"README"`
	MainBranch string   `env:"MAIN_BRANCH, default=main"`
}

type Server struct {
	ListenAddr         string `env:"LISTEN_ADDR, default=0.0.0.0:5555"`
	InternalListenAddr string `env:"INTERNAL_LISTEN_ADDR, default=127.0.0.1:5444"`
	DBPath             string `env:"DB_PATH, default=knotserver.db"`
	Hostname           string `env:"HOSTNAME, required"`
	JetstreamEndpoint  string `env:"JETSTREAM_ENDPOINT, default=wss://jetstream1.us-west.bsky.network/subscribe"`
	Owner              string `env:"OWNER, required"`
	LogDids            bool   `env:"LOG_DIDS, default=true"`

	// This disables signature verification so use with caution.
	Dev bool `env:"DEV, default=false"`
}

func (s Server) Did() syntax.DID {
	return syntax.DID(fmt.Sprintf("did:web:%s", s.Hostname))
}

type Config struct {
	Repo            Repo   `env:",prefix=KNOT_REPO_"`
	Server          Server `env:",prefix=KNOT_SERVER_"`
	AppViewEndpoint string `env:"APPVIEW_ENDPOINT, default=https://tangled.sh"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	if cfg.Repo.Readme == nil {
		cfg.Repo.Readme = []string{
			"README.md", "readme.md",
			"README",
			"readme",
			"README.markdown",
			"readme.markdown",
			"README.txt",
			"readme.txt",
			"README.rst",
			"readme.rst",
			"README.org",
			"readme.org",
			"README.asciidoc",
			"readme.asciidoc",
		}
	}

	return &cfg, nil
}
