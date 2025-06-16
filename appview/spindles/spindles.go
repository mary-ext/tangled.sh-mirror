package spindles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/rbac"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

type Spindles struct {
	Db       *db.DB
	OAuth    *oauth.OAuth
	Pages    *pages.Pages
	Config   *config.Config
	Enforcer *rbac.Enforcer
	Logger   *slog.Logger
}

func (s *Spindles) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.AuthMiddleware(s.OAuth))

	r.Get("/", s.spindles)
	r.Post("/register", s.register)
	r.Delete("/{instance}", s.delete)
	r.Post("/{instance}/retry", s.retry)

	return r
}

func (s *Spindles) spindles(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	all, err := db.GetSpindles(
		s.Db,
		db.FilterEq("owner", user.Did),
	)
	if err != nil {
		s.Logger.Error("failed to fetch spindles", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.Pages.Spindles(w, pages.SpindlesParams{
		LoggedInUser: user,
		Spindles:     all,
	})
}

// this endpoint inserts a record on behalf of the user to register that domain
//
// when registered, it also makes a request to see if the spindle declares this users as its owner,
// and if so, marks the spindle as verified.
//
// if the spindle is not up yet, the user is free to retry verification at a later point
func (s *Spindles) register(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "register")

	noticeId := "register-error"
	defaultErr := "Failed to register spindle. Try again later."
	fail := func() {
		s.Pages.Notice(w, noticeId, defaultErr)
	}

	instance := r.FormValue("instance")
	if instance == "" {
		s.Pages.Notice(w, noticeId, "Incomplete form.")
		return
	}

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start transaction", "err", err)
		fail()
		return
	}
	defer tx.Rollback()

	err = db.AddSpindle(tx, db.Spindle{
		Owner:    syntax.DID(user.Did),
		Instance: instance,
	})
	if err != nil {
		l.Error("failed to insert", "err", err)
		fail()
		return
	}

	// create record on pds
	client, err := s.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.SpindleNSID,
		Repo:       user.Did,
		Rkey:       instance,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.Spindle{
				CreatedAt: time.Now().Format(time.RFC3339),
			},
		},
	})
	if err != nil {
		l.Error("failed to put record", "err", err)
		fail()
		return
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit transaction", "err", err)
		fail()
		return
	}

	// begin verification
	expectedOwner, err := fetchOwner(r.Context(), instance, s.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)

		// just refresh the page
		s.Pages.HxRefresh(w)
		return
	}

	if expectedOwner != user.Did {
		// verification failed
		l.Error("verification failed", "expectedOwner", expectedOwner, "observedOwner", user.Did)
		s.Pages.HxRefresh(w)
		return
	}

	tx, err = s.Db.Begin()
	if err != nil {
		l.Error("failed to commit verification info", "err", err)
		s.Pages.HxRefresh(w)
		return
	}
	defer func() {
		tx.Rollback()
		s.Enforcer.E.LoadPolicy()
	}()

	// mark this spindle as verified in the db
	_, err = db.VerifySpindle(
		tx,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)

	err = s.Enforcer.AddSpindleOwner(instance, user.Did)
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit verification info", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	err = s.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	// ok
	s.Pages.HxRefresh(w)
	return
}

func (s *Spindles) delete(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "register")

	noticeId := "operation-error"
	defaultErr := "Failed to delete spindle. Try again later."
	fail := func() {
		s.Pages.Notice(w, noticeId, defaultErr)
	}

	instance := chi.URLParam(r, "instance")
	if instance == "" {
		l.Error("empty instance")
		fail()
		return
	}

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start txn", "err", err)
		fail()
		return
	}
	defer tx.Rollback()

	err = db.DeleteSpindle(
		tx,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil {
		l.Error("failed to delete spindle", "err", err)
		fail()
		return
	}

	client, err := s.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.SpindleNSID,
		Repo:       user.Did,
		Rkey:       instance,
	})
	if err != nil {
		// non-fatal
		l.Error("failed to delete record", "err", err)
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to delete spindle", "err", err)
		fail()
		return
	}

	w.Write([]byte{})
}

func (s *Spindles) retry(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "register")

	noticeId := "operation-error"
	defaultErr := "Failed to verify spindle. Try again later."
	fail := func() {
		s.Pages.Notice(w, noticeId, defaultErr)
	}

	instance := chi.URLParam(r, "instance")
	if instance == "" {
		l.Error("empty instance")
		fail()
		return
	}

	// begin verification
	expectedOwner, err := fetchOwner(r.Context(), instance, s.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)
		fail()
		return
	}

	if expectedOwner != user.Did {
		l.Error("verification failed", "expectedOwner", expectedOwner, "observedOwner", user.Did)
		s.Pages.Notice(w, noticeId, fmt.Sprintf("Owner did not match, expected %s, got %s", expectedOwner, user.Did))
		return
	}

	// mark this spindle as verified in the db
	rowId, err := db.VerifySpindle(
		s.Db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil {
		l.Error("verification failed", "err", err)
		fail()
		return
	}

	verifiedSpindle := db.Spindle{
		Id:       int(rowId),
		Owner:    syntax.DID(user.Did),
		Instance: instance,
	}

	w.Header().Set("HX-Reswap", "outerHTML")
	s.Pages.SpindleListing(w, pages.SpindleListingParams{
		LoggedInUser: user,
		Spindle:      verifiedSpindle,
	})
}

func fetchOwner(ctx context.Context, domain string, dev bool) (string, error) {
	scheme := "https"
	if dev {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/owner", scheme, domain)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 {
		return "", errors.New("failed to fetch /owner")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)) // read atmost 1kb of data
	if err != nil {
		return "", fmt.Errorf("failed to read /owner response: %w", err)
	}

	did := strings.TrimSpace(string(body))
	if did == "" {
		return "", errors.New("empty DID in /owner response")
	}

	return did, nil
}
