package issue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	issues_indexer "tangled.org/core/appview/indexer/issues"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/appview/session"
	"tangled.org/core/appview/validator"
	"tangled.org/core/idresolver"
	"tangled.org/core/tid"
)

type Service struct {
	config     *config.Config
	db         *db.DB
	indexer    *issues_indexer.Indexer
	logger     *slog.Logger
	notifier   notify.Notifier
	idResolver *idresolver.Resolver
	validator  *validator.Validator
}

func NewService(
	logger *slog.Logger,
	config *config.Config,
	db *db.DB,
	notifier notify.Notifier,
	idResolver *idresolver.Resolver,
	indexer *issues_indexer.Indexer,
	validator *validator.Validator,
) Service {
	return Service{
		config,
		db,
		indexer,
		logger,
		notifier,
		idResolver,
		validator,
	}
}

var (
	ErrUnAuthorized   = errors.New("unauthorized operation")
	ErrDatabaseFail   = errors.New("db op fail")
	ErrPDSFail        = errors.New("pds op fail")
	ErrValidationFail = errors.New("issue validation fail")
)

func (s *Service) NewIssue(ctx context.Context, repo *models.Repo, title, body string) (*models.Issue, error) {
	l := s.logger.With("method", "NewIssue")
	sess := session.FromContext(ctx)
	if sess == nil {
		l.Error("user session is missing in context")
		return nil, ErrUnAuthorized
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	// mentions, references := s.refResolver.Resolve(ctx, body)
	mentions := func() []syntax.DID {
		rawMentions := markup.FindUserMentions(body)
		idents := s.idResolver.ResolveIdents(ctx, rawMentions)
		l.Debug("parsed mentions", "raw", rawMentions, "idents", idents)
		var mentions []syntax.DID
		for _, ident := range idents {
			if ident != nil && !ident.Handle.IsInvalidHandle() {
				mentions = append(mentions, ident.DID)
			}
		}
		return mentions
	}()

	issue := models.Issue{
		RepoAt:  repo.RepoAt(),
		Rkey:    tid.TID(),
		Title:   title,
		Body:    body,
		Open:    true,
		Did:     authorDid.String(),
		Created: time.Now(),
		Repo:    repo,
	}

	if err := s.validator.ValidateIssue(&issue); err != nil {
		l.Error("validation error", "err", err)
		return nil, ErrValidationFail
	}

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

func (s *Service) GetIssues(ctx context.Context, repo *models.Repo, searchOpts models.IssueSearchOptions) ([]models.Issue, error) {
	l := s.logger.With("method", "EditIssue")

	var issues []models.Issue
	var err error
	if searchOpts.Keyword != "" {
		res, err := s.indexer.Search(ctx, searchOpts)
		if err != nil {
			l.Error("failed to search for issues", "err", err)
			return nil, err
		}
		l.Debug("searched issues with indexer", "count", len(res.Hits))
		issues, err = db.GetIssues(s.db, db.FilterIn("id", res.Hits))
		if err != nil {
			l.Error("failed to get issues", "err", err)
			return nil, err
		}
	} else {
		openInt := 0
		if searchOpts.IsOpen {
			openInt = 1
		}
		issues, err = db.GetIssuesPaginated(
			s.db,
			searchOpts.Page,
			db.FilterEq("repo_at", repo.RepoAt()),
			db.FilterEq("open", openInt),
		)
		if err != nil {
			l.Error("failed to get issues", "err", err)
			return nil, err
		}
	}

	return issues, nil
}

func (s *Service) EditIssue(ctx context.Context, issue *models.Issue) error {
	l := s.logger.With("method", "EditIssue")
	sess := session.FromContext(ctx)
	if sess == nil {
		l.Error("user session is missing in context")
		return ErrUnAuthorized
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	if err := s.validator.ValidateIssue(issue); err != nil {
		l.Error("validation error", "err", err)
		return ErrValidationFail
	}

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

func (s *Service) DeleteIssue(ctx context.Context, issue *models.Issue) error {
	l := s.logger.With("method", "DeleteIssue")
	sess := session.FromContext(ctx)
	if sess == nil {
		l.Error("user session is missing in context")
		return ErrUnAuthorized
	}
	authorDid := sess.Data.AccountDID
	l = l.With("did", authorDid)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		l.Error("db.BeginTx failed", "err", err)
		return ErrDatabaseFail
	}
	defer tx.Rollback()

	if err := db.DeleteIssues(tx, db.FilterEq("id", issue.Id)); err != nil {
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
