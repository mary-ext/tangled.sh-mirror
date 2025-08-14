package spindles

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/serververify"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/tid"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

type Spindles struct {
	Db         *db.DB
	OAuth      *oauth.OAuth
	Pages      *pages.Pages
	Config     *config.Config
	Enforcer   *rbac.Enforcer
	IdResolver *idresolver.Resolver
	Logger     *slog.Logger
}

func (s *Spindles) Router() http.Handler {
	r := chi.NewRouter()

	r.With(middleware.AuthMiddleware(s.OAuth)).Get("/", s.spindles)
	r.With(middleware.AuthMiddleware(s.OAuth)).Post("/register", s.register)

	r.With(middleware.AuthMiddleware(s.OAuth)).Get("/{instance}", s.dashboard)
	r.With(middleware.AuthMiddleware(s.OAuth)).Delete("/{instance}", s.delete)

	r.With(middleware.AuthMiddleware(s.OAuth)).Post("/{instance}/retry", s.retry)
	r.With(middleware.AuthMiddleware(s.OAuth)).Post("/{instance}/add", s.addMember)
	r.With(middleware.AuthMiddleware(s.OAuth)).Post("/{instance}/remove", s.removeMember)

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

func (s *Spindles) dashboard(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "dashboard")

	user := s.OAuth.GetUser(r)
	l = l.With("user", user.Did)

	instance := chi.URLParam(r, "instance")
	if instance == "" {
		return
	}
	l = l.With("instance", instance)

	spindles, err := db.GetSpindles(
		s.Db,
		db.FilterEq("instance", instance),
		db.FilterEq("owner", user.Did),
		db.FilterIsNot("verified", "null"),
	)
	if err != nil || len(spindles) != 1 {
		l.Error("failed to get spindle", "err", err, "len(spindles)", len(spindles))
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	spindle := spindles[0]
	members, err := s.Enforcer.GetSpindleUsersByRole("server:member", spindle.Instance)
	if err != nil {
		l.Error("failed to get spindle members", "err", err)
		http.Error(w, "Not found", http.StatusInternalServerError)
		return
	}
	slices.Sort(members)

	repos, err := db.GetRepos(
		s.Db,
		0,
		db.FilterEq("spindle", instance),
	)
	if err != nil {
		l.Error("failed to get spindle repos", "err", err)
		http.Error(w, "Not found", http.StatusInternalServerError)
		return
	}

	// organize repos by did
	repoMap := make(map[string][]db.Repo)
	for _, r := range repos {
		repoMap[r.Did] = append(repoMap[r.Did], r)
	}

	s.Pages.SpindleDashboard(w, pages.SpindleDashboardParams{
		LoggedInUser: user,
		Spindle:      spindle,
		Members:      members,
		Repos:        repoMap,
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
	l = l.With("instance", instance)
	l = l.With("user", user.Did)

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start transaction", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		s.Enforcer.E.LoadPolicy()
	}()

	err = db.AddSpindle(tx, db.Spindle{
		Owner:    syntax.DID(user.Did),
		Instance: instance,
	})
	if err != nil {
		l.Error("failed to insert", "err", err)
		fail()
		return
	}

	err = s.Enforcer.AddSpindle(instance)
	if err != nil {
		l.Error("failed to create spindle", "err", err)
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

	ex, _ := client.RepoGetRecord(r.Context(), "", tangled.SpindleNSID, user.Did, instance)
	var exCid *string
	if ex != nil {
		exCid = ex.Cid
	}

	// re-announce by registering under same rkey
	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.SpindleNSID,
		Repo:       user.Did,
		Rkey:       instance,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.Spindle{
				CreatedAt: time.Now().Format(time.RFC3339),
			},
		},
		SwapRecord: exCid,
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

	err = s.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	// begin verification
	err = serververify.RunVerification(r.Context(), instance, user.Did, s.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	_, err = serververify.MarkSpindleVerified(s.Db, s.Enforcer, instance, user.Did)
	if err != nil {
		l.Error("failed to mark verified", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	// ok
	s.Pages.HxRefresh(w)
}

func (s *Spindles) delete(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "delete")

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

	spindles, err := db.GetSpindles(
		s.Db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil || len(spindles) != 1 {
		l.Error("failed to retrieve instance", "err", err, "len(spindles)", len(spindles))
		fail()
		return
	}

	if string(spindles[0].Owner) != user.Did {
		l.Error("unauthorized", "user", user.Did, "owner", spindles[0].Owner)
		s.Pages.Notice(w, noticeId, "Failed to delete spindle, unauthorized deletion attempt.")
		return
	}

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start txn", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		s.Enforcer.E.LoadPolicy()
	}()

	// remove spindle members first
	err = db.RemoveSpindleMember(
		tx,
		db.FilterEq("did", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil {
		l.Error("failed to remove spindle members", "err", err)
		fail()
		return
	}

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

	// delete from enforcer
	if spindles[0].Verified != nil {
		err = s.Enforcer.RemoveSpindle(instance)
		if err != nil {
			l.Error("failed to update ACL", "err", err)
			fail()
			return
		}
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

	err = s.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	shouldRedirect := r.Header.Get("shouldRedirect")
	if shouldRedirect == "true" {
		s.Pages.HxRedirect(w, "/spindles")
		return
	}

	w.Write([]byte{})
}

func (s *Spindles) retry(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "retry")

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
	l = l.With("instance", instance)
	l = l.With("user", user.Did)

	spindles, err := db.GetSpindles(
		s.Db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil || len(spindles) != 1 {
		l.Error("failed to retrieve instance", "err", err, "len(spindles)", len(spindles))
		fail()
		return
	}

	if string(spindles[0].Owner) != user.Did {
		l.Error("unauthorized", "user", user.Did, "owner", spindles[0].Owner)
		s.Pages.Notice(w, noticeId, "Failed to verify spindle, unauthorized verification attempt.")
		return
	}

	// begin verification
	err = serververify.RunVerification(r.Context(), instance, user.Did, s.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)

		if errors.Is(err, serververify.FetchError) {
			s.Pages.Notice(w, noticeId, "Failed to verify knot, unable to fetch owner.")
			return
		}

		if e, ok := err.(*serververify.OwnerMismatch); ok {
			s.Pages.Notice(w, noticeId, e.Error())
			return
		}

		fail()
		return
	}

	rowId, err := serververify.MarkSpindleVerified(s.Db, s.Enforcer, instance, user.Did)
	if err != nil {
		l.Error("failed to mark verified", "err", err)
		s.Pages.Notice(w, noticeId, err.Error())
		return
	}

	verifiedSpindle, err := db.GetSpindles(
		s.Db,
		db.FilterEq("id", rowId),
	)
	if err != nil || len(verifiedSpindle) != 1 {
		l.Error("failed get new spindle", "err", err)
		s.Pages.HxRefresh(w)
		return
	}

	shouldRefresh := r.Header.Get("shouldRefresh")
	if shouldRefresh == "true" {
		s.Pages.HxRefresh(w)
		return
	}

	w.Header().Set("HX-Reswap", "outerHTML")
	s.Pages.SpindleListing(w, pages.SpindleListingParams{verifiedSpindle[0]})
}

func (s *Spindles) addMember(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "addMember")

	instance := chi.URLParam(r, "instance")
	if instance == "" {
		l.Error("empty instance")
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	l = l.With("instance", instance)
	l = l.With("user", user.Did)

	spindles, err := db.GetSpindles(
		s.Db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil || len(spindles) != 1 {
		l.Error("failed to retrieve instance", "err", err, "len(spindles)", len(spindles))
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	noticeId := fmt.Sprintf("add-member-error-%d", spindles[0].Id)
	defaultErr := "Failed to add member. Try again later."
	fail := func() {
		s.Pages.Notice(w, noticeId, defaultErr)
	}

	if string(spindles[0].Owner) != user.Did {
		l.Error("unauthorized", "user", user.Did, "owner", spindles[0].Owner)
		s.Pages.Notice(w, noticeId, "Failed to add member, unauthorized attempt.")
		return
	}

	member := r.FormValue("member")
	if member == "" {
		l.Error("empty member")
		s.Pages.Notice(w, noticeId, "Failed to add member, empty form.")
		return
	}
	l = l.With("member", member)

	memberId, err := s.IdResolver.ResolveIdent(r.Context(), member)
	if err != nil {
		l.Error("failed to resolve member identity to handle", "err", err)
		s.Pages.Notice(w, noticeId, "Failed to add member, identity resolution failed.")
		return
	}
	if memberId.Handle.IsInvalidHandle() {
		l.Error("failed to resolve member identity to handle")
		s.Pages.Notice(w, noticeId, "Failed to add member, identity resolution failed.")
		return
	}

	// write to pds
	client, err := s.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start txn", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		s.Enforcer.E.LoadPolicy()
	}()

	rkey := tid.TID()

	// add member to db
	if err = db.AddSpindleMember(tx, db.SpindleMember{
		Did:      syntax.DID(user.Did),
		Rkey:     rkey,
		Instance: instance,
		Subject:  memberId.DID,
	}); err != nil {
		l.Error("failed to add spindle member", "err", err)
		fail()
		return
	}

	if err = s.Enforcer.AddSpindleMember(instance, memberId.DID.String()); err != nil {
		l.Error("failed to add member to ACLs")
		fail()
		return
	}

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.SpindleMemberNSID,
		Repo:       user.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.SpindleMember{
				CreatedAt: time.Now().Format(time.RFC3339),
				Instance:  instance,
				Subject:   memberId.DID.String(),
			},
		},
	})
	if err != nil {
		l.Error("failed to add record to PDS", "err", err)
		s.Pages.Notice(w, noticeId, "Failed to add record to PDS, try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		l.Error("failed to commit txn", "err", err)
		fail()
		return
	}

	if err = s.Enforcer.E.SavePolicy(); err != nil {
		l.Error("failed to add member to ACLs", "err", err)
		fail()
		return
	}

	// success
	s.Pages.HxRedirect(w, fmt.Sprintf("/spindles/%s", instance))
}

func (s *Spindles) removeMember(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	l := s.Logger.With("handler", "removeMember")

	noticeId := "operation-error"
	defaultErr := "Failed to remove member. Try again later."
	fail := func() {
		s.Pages.Notice(w, noticeId, defaultErr)
	}

	instance := chi.URLParam(r, "instance")
	if instance == "" {
		l.Error("empty instance")
		fail()
		return
	}
	l = l.With("instance", instance)
	l = l.With("user", user.Did)

	spindles, err := db.GetSpindles(
		s.Db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("instance", instance),
	)
	if err != nil || len(spindles) != 1 {
		l.Error("failed to retrieve instance", "err", err, "len(spindles)", len(spindles))
		fail()
		return
	}

	if string(spindles[0].Owner) != user.Did {
		l.Error("unauthorized", "user", user.Did, "owner", spindles[0].Owner)
		s.Pages.Notice(w, noticeId, "Failed to remove member, unauthorized attempt.")
		return
	}

	member := r.FormValue("member")
	if member == "" {
		l.Error("empty member")
		s.Pages.Notice(w, noticeId, "Failed to remove member, empty form.")
		return
	}
	l = l.With("member", member)

	memberId, err := s.IdResolver.ResolveIdent(r.Context(), member)
	if err != nil {
		l.Error("failed to resolve member identity to handle", "err", err)
		s.Pages.Notice(w, noticeId, "Failed to remove member, identity resolution failed.")
		return
	}
	if memberId.Handle.IsInvalidHandle() {
		l.Error("failed to resolve member identity to handle")
		s.Pages.Notice(w, noticeId, "Failed to remove member, identity resolution failed.")
		return
	}

	tx, err := s.Db.Begin()
	if err != nil {
		l.Error("failed to start txn", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		s.Enforcer.E.LoadPolicy()
	}()

	// get the record from the DB first:
	members, err := db.GetSpindleMembers(
		s.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("instance", instance),
		db.FilterEq("subject", memberId.DID),
	)
	if err != nil || len(members) != 1 {
		l.Error("failed to get member", "err", err)
		fail()
		return
	}

	// remove from db
	if err = db.RemoveSpindleMember(
		tx,
		db.FilterEq("did", user.Did),
		db.FilterEq("instance", instance),
		db.FilterEq("subject", memberId.DID),
	); err != nil {
		l.Error("failed to remove spindle member", "err", err)
		fail()
		return
	}

	// remove from enforcer
	if err = s.Enforcer.RemoveSpindleMember(instance, memberId.DID.String()); err != nil {
		l.Error("failed to update ACLs", "err", err)
		fail()
		return
	}

	client, err := s.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	// remove from pds
	_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.SpindleMemberNSID,
		Repo:       user.Did,
		Rkey:       members[0].Rkey,
	})
	if err != nil {
		// non-fatal
		l.Error("failed to delete record", "err", err)
	}

	// commit everything
	if err = tx.Commit(); err != nil {
		l.Error("failed to commit txn", "err", err)
		fail()
		return
	}

	// commit everything
	if err = s.Enforcer.E.SavePolicy(); err != nil {
		l.Error("failed to save ACLs", "err", err)
		fail()
		return
	}

	// ok
	s.Pages.HxRefresh(w)
}
