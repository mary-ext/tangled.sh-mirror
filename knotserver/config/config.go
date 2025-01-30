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

type Config struct {
	Host   string `env:"KNOTSERVER_HOST, default=0.0.0.0"`
	Port   int    `env:"KNOTSERVER_PORT, default=5555"`
	Secret string `env:"KNOTSERVER_SECRET, required"`

	Repo Repo `env:",prefix=KNOTSERVER_REPO_"`
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
