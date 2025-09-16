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
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/validator"
	"tangled.sh/tangled.sh/core/appview/xrpcclient"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/tid"
)

type Labels struct {
	oauth     *oauth.OAuth
	pages     *pages.Pages
	db        *db.DB
	logger    *slog.Logger
	validator *validator.Validator
}

func New(
	oauth *oauth.OAuth,
	pages *pages.Pages,
	db *db.DB,
	validator *validator.Validator,
) *Labels {
	logger := log.New("labels")

	return &Labels{
		oauth:     oauth,
		pages:     pages,
		db:        db,
		logger:    logger,
		validator: validator,
	}
}

func (l *Labels) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.AuthMiddleware(l.oauth))
	r.Put("/perform", l.PerformLabelOp)

	return r
}

// this is a tricky handler implementation:
// - the user selects the new state of all the labels in the label panel and hits save
// - this handler should calculate the diff in order to create the labelop record
// - we need the diff in order to maintain a "history" of operations performed by users
func (l *Labels) PerformLabelOp(w http.ResponseWriter, r *http.Request) {
	user := l.oauth.GetUser(r)

	noticeId := "add-label-error"

	fail := func(msg string, err error) {
		l.logger.Error("failed to add label", "err", err)
		l.pages.Notice(w, noticeId, msg)
	}

	if err := r.ParseForm(); err != nil {
		fail("Invalid form.", err)
		return
	}

	did := user.Did
	rkey := tid.TID()
	performedAt := time.Now()
	indexedAt := time.Now()
	repoAt := r.Form.Get("repo")
	subjectUri := r.Form.Get("subject")

	// find all the labels that this repo subscribes to
	repoLabels, err := db.GetRepoLabels(l.db, db.FilterEq("repo_at", repoAt))
	if err != nil {
		fail("Failed to get labels for this repository.", err)
		return
	}

	var labelAts []string
	for _, rl := range repoLabels {
		labelAts = append(labelAts, rl.LabelAt.String())
	}

	actx, err := db.NewLabelApplicationCtx(l.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		fail("Invalid form data.", err)
		return
	}

	// calculate the start state by applying already known labels
	existingOps, err := db.GetLabelOps(l.db, db.FilterEq("subject", subjectUri))
	if err != nil {
		fail("Invalid form data.", err)
		return
	}

	labelState := db.NewLabelState()
	actx.ApplyLabelOps(labelState, existingOps)

	var labelOps []db.LabelOp

	// first delete all existing state
	for key, vals := range labelState.Inner() {
		for val := range vals {
			labelOps = append(labelOps, db.LabelOp{
				Did:          did,
				Rkey:         rkey,
				Subject:      syntax.ATURI(subjectUri),
				Operation:    db.LabelOperationDel,
				OperandKey:   key,
				OperandValue: val,
				PerformedAt:  performedAt,
				IndexedAt:    indexedAt,
			})
		}
	}

	// add all the new state the user specified
	for key, vals := range r.Form {
		if _, ok := actx.Defs[key]; !ok {
			continue
		}

		for _, val := range vals {
			labelOps = append(labelOps, db.LabelOp{
				Did:          did,
				Rkey:         rkey,
				Subject:      syntax.ATURI(subjectUri),
				Operation:    db.LabelOperationAdd,
				OperandKey:   key,
				OperandValue: val,
				PerformedAt:  performedAt,
				IndexedAt:    indexedAt,
			})
		}
	}

	// reduce the opset
	labelOps = db.ReduceLabelOps(labelOps)

	for i := range labelOps {
		def := actx.Defs[labelOps[i].OperandKey]
		if err := l.validator.ValidateLabelOp(def, &labelOps[i]); err != nil {
			fail(fmt.Sprintf("Invalid form data: %s", err), err)
			return
		}
	}

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
		fail("Failed to authorize user.", err)
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
		fail("Failed to create record on PDS for user.", err)
		return
	}
	atUri := resp.Uri

	tx, err := l.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to update labels. Try again later.", err)
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
			fail("Failed to update labels. Try again later.", err)
			return
		}
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
