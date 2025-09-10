package labels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/appview/xrpcclient"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/tid"
)

type Labels struct {
	repoResolver *reporesolver.RepoResolver
	idResolver   *idresolver.Resolver
	oauth        *oauth.OAuth
	pages        *pages.Pages
	db           *db.DB
	logger       *slog.Logger
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	spindlestream *eventconsumer.Consumer,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	enforcer *rbac.Enforcer,
) *Labels {
	logger := log.New("labels")

	return &Labels{
		oauth:        oauth,
		repoResolver: repoResolver,
		pages:        pages,
		idResolver:   idResolver,
		db:           db,
		logger:       logger,
	}
}

func (l *Labels) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.With(middleware.AuthMiddleware(l.oauth)).Put("/perform", l.PerformLabelOp)

	return r
}

func (l *Labels) PerformLabelOp(w http.ResponseWriter, r *http.Request) {
	user := l.oauth.GetUser(r)

	if err := r.ParseForm(); err != nil {
		l.logger.Error("failed to parse form data", "error", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	did := user.Did
	rkey := tid.TID()
	performedAt := time.Now()
	indexedAt := time.Now()
	repoAt := r.Form.Get("repo")
	subjectUri := r.Form.Get("subject")
	keys := r.Form["operand-key"]
	vals := r.Form["operand-val"]

	var labelOps []db.LabelOp
	for i := range len(keys) {
		op := r.FormValue(fmt.Sprintf("op-%d", i))
		if op == "" {
			op = string(db.LabelOperationDel)
		}
		key := keys[i]
		val := vals[i]

		labelOps = append(labelOps, db.LabelOp{
			Did:          did,
			Rkey:         rkey,
			Subject:      syntax.ATURI(subjectUri),
			Operation:    db.LabelOperation(op),
			OperandKey:   key,
			OperandValue: val,
			PerformedAt:  performedAt,
			IndexedAt:    indexedAt,
		})
	}

	// TODO: validate the operations

	// find all the labels that this repo subscribes to
	repoLabels, err := db.GetRepoLabels(l.db, db.FilterEq("repo_at", repoAt))
	if err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	var labelAts []string
	for _, rl := range repoLabels {
		labelAts = append(labelAts, rl.LabelAt.String())
	}

	actx, err := db.NewLabelApplicationCtx(l.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// calculate the start state by applying already known labels
	existingOps, err := db.GetLabelOps(l.db, db.FilterEq("subject", subjectUri))
	if err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	labelState := db.NewLabelState()
	actx.ApplyLabelOps(labelState, existingOps)

	// next, apply all ops introduced in this request and filter out ones that are no-ops
	validLabelOps := labelOps[:0]
	for _, op := range labelOps {
		if err = actx.ApplyLabelOp(labelState, op); err != db.LabelNoOpError {
			validLabelOps = append(validLabelOps, op)
		}
	}

	// nothing to do
	if len(validLabelOps) == 0 {
		l.pages.HxRefresh(w)
		return
	}

	// create an atproto record of valid ops
	record := db.LabelOpsAsRecord(validLabelOps)

	client, err := l.oauth.AuthorizedClient(r)
	if err != nil {
		l.logger.Error("failed to create client", "error", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.LabelOpNSID,
		Repo:       did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})
	if err != nil {
		l.logger.Error("failed to write to PDS", "error", err)
		http.Error(w, "failed to write to PDS", http.StatusInternalServerError)
		return
	}
	atUri := resp.Uri

	tx, err := l.db.BeginTx(r.Context(), nil)
	if err != nil {
		l.logger.Error("failed to start tx", "error", err)
		return
	}

	rollback := func() {
		err1 := tx.Rollback()
		err2 := rollbackRecord(context.Background(), atUri, client)

		// ignore txn complete errors, this is okay
		if errors.Is(err1, sql.ErrTxDone) {
			err1 = nil
		}

		if errs := errors.Join(err1, err2); errs != nil {
			return
		}
	}
	defer rollback()

	for _, o := range validLabelOps {
		if _, err := db.AddLabelOp(l.db, &o); err != nil {
			l.logger.Error("failed to add op", "err", err)
			return
		}

		l.logger.Info("performed label op", "did", o.Did, "rkey", o.Rkey, "kind", o.Operation, "subjcet", o.Subject, "key", o.OperandKey)
	}

	err = tx.Commit()
	if err != nil {
		return
	}

	// clear aturi when everything is successful
	atUri = ""

	l.pages.HxRefresh(w)
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, xrpcc *xrpcclient.Client) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := xrpcc.RepoDeleteRecord(ctx, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}
