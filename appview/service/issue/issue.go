package issue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/refresolver"
	"tangled.org/core/tid"
)

type IssueService struct {
	logger      *slog.Logger
	config      *config.Config
	db          *db.DB
	notifier    notify.Notifier
	refResolver *refresolver.Resolver
}

func NewService(
	logger *slog.Logger,
	config *config.Config,
	db *db.DB,
	notifier notify.Notifier,
	refResolver *refresolver.Resolver,
) IssueService {
	return IssueService{
		logger,
		config,
		db,
		notifier,
		refResolver,
	}
}

var (
	ErrCtxMissing     = errors.New("context values are missing")
	ErrDatabaseFail   = errors.New("db op fail")
	ErrPDSFail        = errors.New("pds op fail")
	ErrValidationFail = errors.New("issue validation fail")
)

// TODO: NewIssue should return typed errors
func (s *IssueService) NewIssue(ctx context.Context, repo *models.Repo, title, body string) (*models.Issue, error) {
	l := s.logger.With("method", "NewIssue")
	sess, ok := fromContext(ctx)
	if !ok {
		l.Error("user session is missing in context")
		return nil, ErrCtxMissing
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	mentions, references := s.refResolver.Resolve(ctx, body)

	issue := models.Issue{
		RepoAt:     repo.RepoAt(),
		Rkey:       tid.TID(),
		Title:      title,
		Body:       body,
		Open:       true,
		Did:        authorDid.String(),
		Created:    time.Now(),
		Mentions:   mentions,
		References: references,
		Repo:       repo,
	}
	// TODO: validate the issue

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		l.Error("db.BeginTx failed", "err", err)
		return nil, ErrDatabaseFail
	}
	defer tx.Rollback()

	if err := db.PutIssue(tx, &issue); err != nil {
		l.Error("db.PutIssue failed", "err", err)
		return nil, ErrDatabaseFail
	}

	atpclient := sess.APIClient()
	record := issue.AsRecord()
	_, err = atproto.RepoPutRecord(ctx, atpclient, &atproto.RepoPutRecord_Input{
		Repo:       authorDid.String(),
		Collection: tangled.RepoIssueNSID,
		Rkey:       issue.Rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})
	if err != nil {
		l.Error("atproto.RepoPutRecord failed", "err", err)
		return nil, ErrPDSFail
	}
	if err = tx.Commit(); err != nil {
		l.Error("tx.Commit failed", "err", err)
		return nil, ErrDatabaseFail
	}

	s.notifier.NewIssue(ctx, &issue, mentions)
	return &issue, nil
}

func (s *IssueService) EditIssue(ctx context.Context, issue *models.Issue) error {
	l := s.logger.With("method", "EditIssue")
	sess, ok := fromContext(ctx)
	if !ok {
		l.Error("user session is missing in context")
		return ErrCtxMissing
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	// TODO: validate issue

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		l.Error("db.BeginTx failed", "err", err)
		return ErrDatabaseFail
	}
	defer tx.Rollback()

	if err := db.PutIssue(tx, issue); err != nil {
		l.Error("db.PutIssue failed", "err", err)
		return ErrDatabaseFail
	}

	atpclient := sess.APIClient()
	record := issue.AsRecord()

	ex, err := atproto.RepoGetRecord(ctx, atpclient, "", tangled.RepoIssueNSID, issue.Did, issue.Rkey)
	if err != nil {
		l.Error("atproto.RepoGetRecord failed", "err", err)
		return ErrPDSFail
	}
	_, err = atproto.RepoPutRecord(ctx, atpclient, &atproto.RepoPutRecord_Input{
		Collection: tangled.RepoIssueNSID,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})
	if err != nil {
		l.Error("atproto.RepoPutRecord failed", "err", err)
		return ErrPDSFail
	}

	if err = tx.Commit(); err != nil {
		l.Error("tx.Commit failed", "err", err)
		return ErrDatabaseFail
	}

	// TODO: notify PutIssue

	return nil
}

func (s *IssueService) DeleteIssue(ctx context.Context, issue *models.Issue) error {
	l := s.logger.With("method", "DeleteIssue")
	sess, ok := fromContext(ctx)
	if !ok {
		return ErrCtxMissing
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		l.Error("db.BeginTx failed", "err", err)
		return ErrDatabaseFail
	}
	defer tx.Rollback()

	if err := db.DeleteIssues(tx, issue.Did, issue.Rkey); err != nil {
		l.Error("db.DeleteIssues failed", "err", err)
		return ErrDatabaseFail
	}

	atpclient := sess.APIClient()
	_, err = atproto.RepoDeleteRecord(ctx, atpclient, &atproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoIssueNSID,
		Repo:       issue.Did,
		Rkey:       issue.Rkey,
	})
	if err != nil {
		l.Error("atproto.RepoDeleteRecord failed", "err", err)
		return ErrPDSFail
	}

	if err := tx.Commit(); err != nil {
		l.Error("tx.Commit failed", "err", err)
		return ErrDatabaseFail
	}

	s.notifier.DeleteIssue(ctx, issue)
	return nil
}

// TODO: remove this
func fromContext(ctx context.Context) (*oauth.ClientSession, bool) {
	sess, ok := ctx.Value("sess").(*oauth.ClientSession)
	return sess, ok
}
