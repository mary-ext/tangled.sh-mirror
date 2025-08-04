package knotserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/urfave/cli/v3"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/hook"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:   "server",
		Usage:  "run a knot server",
		Action: Run,
		Description: `
Environment variables:
	KNOT_SERVER_SECRET              (required)
	KNOT_SERVER_HOSTNAME            (required)
	KNOT_SERVER_LISTEN_ADDR         (default: 0.0.0.0:5555)
	KNOT_SERVER_INTERNAL_LISTEN_ADDR (default: 127.0.0.1:5444)
	KNOT_SERVER_DB_PATH             (default: knotserver.db)
	KNOT_SERVER_JETSTREAM_ENDPOINT  (default: wss://jetstream1.us-west.bsky.network/subscribe)
	KNOT_SERVER_DEV                 (default: false)
	KNOT_REPO_SCAN_PATH             (default: /home/git)
	KNOT_REPO_README                (comma-separated list)
	KNOT_REPO_MAIN_BRANCH           (default: main)
	APPVIEW_ENDPOINT                (default: https://tangled.sh)
`,
	}
}

func Run(ctx context.Context, cmd *cli.Command) error {
	logger := log.FromContext(ctx)
	iLogger := log.New("knotserver/internal")

	c, err := config.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	err = hook.Setup(hook.Config(
		hook.WithScanPath(c.Repo.ScanPath),
		hook.WithInternalApi(c.Server.InternalListenAddr),
	))
	if err != nil {
		return fmt.Errorf("failed to setup hooks: %w", err)
	}
	logger.Info("successfully finished setting up hooks")

	if c.Server.Dev {
		logger.Info("running in dev mode, signature verification is disabled")
	}

	db, err := db.Setup(c.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to load db: %w", err)
	}

	e, err := rbac.NewEnforcer(c.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to setup rbac enforcer: %w", err)
	}

	e.E.EnableAutoSave(true)

	jc, err := jetstream.NewJetstreamClient(c.Server.JetstreamEndpoint, "knotserver", []string{
		tangled.PublicKeyNSID,
		tangled.KnotMemberNSID,
		tangled.RepoPullNSID,
		tangled.RepoCollaboratorNSID,
	}, nil, logger, db, true, c.Server.LogDids)
	if err != nil {
		logger.Error("failed to setup jetstream", "error", err)
	}

	notifier := notifier.New()

	mux, err := Setup(ctx, c, db, e, jc, logger, &notifier)
	if err != nil {
		return fmt.Errorf("failed to setup server: %w", err)
	}

	imux := Internal(ctx, c, db, e, iLogger, &notifier)

	logger.Info("starting internal server", "address", c.Server.InternalListenAddr)
	go http.ListenAndServe(c.Server.InternalListenAddr, imux)

	logger.Info("starting main server", "address", c.Server.ListenAddr)
	logger.Error("server error", "error", http.ListenAndServe(c.Server.ListenAddr, mux))

	return nil
}
