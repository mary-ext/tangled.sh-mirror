package repo

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"
)

type RepoService struct {
	logger   *slog.Logger
	config   *config.Config
	db       *db.DB
	enforcer *rbac.Enforcer
}

// NewRepo creates a repository
// It expects atproto session to be passed in `ctx`
func (s *RepoService) NewRepo(ctx context.Context, name, description, knot string) error {
	l := s.logger.With("method", "NewRepo")
	sess := fromContext(ctx)

	ownerDid := sess.Data.AccountDID
	l = l.With("did", ownerDid)

	repo := models.Repo{
		Did:         ownerDid.String(),
		Name:        name,
		Knot:        knot,
		Rkey:        tid.TID(),
		Description: description,
		Created:     time.Now(),
		Labels:      s.config.Label.DefaultLabelDefs,
	}
	l = l.With("aturi", repo.RepoAt())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db.BeginTx: %w", err)
	}
	defer tx.Rollback()


	atpclient := sess.APIClient()
	_, err = atproto.RepoPutRecord(ctx, atpclient, &atproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo: repo.Did,
	})
	if err != nil {
		return fmt.Errorf("atproto.RepoPutRecord: %w", err)
	}
	l.Info("wrote to PDS")

	// knotclient, err := s.oauth.ServiceClient(
	// )

	return nil
}

func fromContext(ctx context.Context) oauth.ClientSession {
	panic("todo")
}
