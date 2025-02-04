package config

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Repo struct {
	ScanPath   string   `env:"SCAN_PATH, default=/home/git"`
	Readme     []string `env:"README"`
	MainBranch []string `env:"MAIN_BRANCH"`
}

type Server struct {
	Host         string `env:"HOST, default=0.0.0.0"`
	Port         int    `env:"PORT, default=5555"`
	InternalPort int    `env:"PORT, default=5444"`
	Secret       string `env:"SECRET, required"`
	DBPath       string `env:"DB_PATH, default=knotserver.db"`
	// This disables signature verification so use with caution.
	Dev bool `env:"DEV, default=false"`
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
