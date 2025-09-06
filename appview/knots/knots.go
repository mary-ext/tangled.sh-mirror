package knots

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
	"tangled.sh/tangled.sh/core/appview/xrpcclient"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/tid"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

type Knots struct {
	Db         *db.DB
	OAuth      *oauth.OAuth
	Pages      *pages.Pages
	Config     *config.Config
	Enforcer   *rbac.Enforcer
	IdResolver *idresolver.Resolver
	Logger     *slog.Logger
	Knotstream *eventconsumer.Consumer
}

func (k *Knots) Router() http.Handler {
	r := chi.NewRouter()

	r.With(middleware.AuthMiddleware(k.OAuth)).Get("/", k.knots)
	r.With(middleware.AuthMiddleware(k.OAuth)).Post("/register", k.register)

	r.With(middleware.AuthMiddleware(k.OAuth)).Get("/{domain}", k.dashboard)
	r.With(middleware.AuthMiddleware(k.OAuth)).Delete("/{domain}", k.delete)

	r.With(middleware.AuthMiddleware(k.OAuth)).Post("/{domain}/retry", k.retry)
	r.With(middleware.AuthMiddleware(k.OAuth)).Post("/{domain}/add", k.addMember)
	r.With(middleware.AuthMiddleware(k.OAuth)).Post("/{domain}/remove", k.removeMember)

	return r
}

func (k *Knots) knots(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
	)
	if err != nil {
		k.Logger.Error("failed to fetch knot registrations", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	k.Pages.Knots(w, pages.KnotsParams{
		LoggedInUser:  user,
		Registrations: registrations,
	})
}

func (k *Knots) dashboard(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "dashboard")

	user := k.OAuth.GetUser(r)
	l = l.With("user", user.Did)

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		return
	}
	l = l.With("domain", domain)

	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
	)
	if err != nil {
		l.Error("failed to get registrations", "err", err)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		return
	}
	registration := registrations[0]

	members, err := k.Enforcer.GetUserByRole("server:member", domain)
	if err != nil {
		l.Error("failed to get knot members", "err", err)
		http.Error(w, "Not found", http.StatusInternalServerError)
		return
	}
	slices.Sort(members)

	repos, err := db.GetRepos(
		k.Db,
		0,
		db.FilterEq("knot", domain),
	)
	if err != nil {
		l.Error("failed to get knot repos", "err", err)
		http.Error(w, "Not found", http.StatusInternalServerError)
		return
	}

	// organize repos by did
	repoMap := make(map[string][]db.Repo)
	for _, r := range repos {
		repoMap[r.Did] = append(repoMap[r.Did], r)
	}

	k.Pages.Knot(w, pages.KnotParams{
		LoggedInUser: user,
		Registration: &registration,
		Members:      members,
		Repos:        repoMap,
		IsOwner:      true,
	})
}

func (k *Knots) register(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	l := k.Logger.With("handler", "register")

	noticeId := "register-error"
	defaultErr := "Failed to register knot. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	domain := r.FormValue("domain")
	if domain == "" {
		k.Pages.Notice(w, noticeId, "Incomplete form.")
		return
	}
	l = l.With("domain", domain)
	l = l.With("user", user.Did)

	tx, err := k.Db.Begin()
	if err != nil {
		l.Error("failed to start transaction", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		k.Enforcer.E.LoadPolicy()
	}()

	err = db.AddKnot(tx, domain, user.Did)
	if err != nil {
		l.Error("failed to insert", "err", err)
		fail()
		return
	}

	err = k.Enforcer.AddKnot(domain)
	if err != nil {
		l.Error("failed to create knot", "err", err)
		fail()
		return
	}

	// create record on pds
	client, err := k.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	ex, _ := client.RepoGetRecord(r.Context(), "", tangled.KnotNSID, user.Did, domain)
	var exCid *string
	if ex != nil {
		exCid = ex.Cid
	}

	// re-announce by registering under same rkey
	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.KnotNSID,
		Repo:       user.Did,
		Rkey:       domain,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.Knot{
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

	err = k.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		k.Pages.HxRefresh(w)
		return
	}

	// begin verification
	err = serververify.RunVerification(r.Context(), domain, user.Did, k.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)
		k.Pages.HxRefresh(w)
		return
	}

	err = serververify.MarkKnotVerified(k.Db, k.Enforcer, domain, user.Did)
	if err != nil {
		l.Error("failed to mark verified", "err", err)
		k.Pages.HxRefresh(w)
		return
	}

	// add this knot to knotstream
	go k.Knotstream.AddSource(
		r.Context(),
		eventconsumer.NewKnotSource(domain),
	)

	// ok
	k.Pages.HxRefresh(w)
}

func (k *Knots) delete(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	l := k.Logger.With("handler", "delete")

	noticeId := "operation-error"
	defaultErr := "Failed to delete knot. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		l.Error("empty domain")
		fail()
		return
	}

	// get record from db first
	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
	)
	if err != nil {
		l.Error("failed to get registration", "err", err)
		fail()
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		fail()
		return
	}
	registration := registrations[0]

	tx, err := k.Db.Begin()
	if err != nil {
		l.Error("failed to start txn", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		k.Enforcer.E.LoadPolicy()
	}()

	err = db.DeleteKnot(
		tx,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
	)
	if err != nil {
		l.Error("failed to delete registration", "err", err)
		fail()
		return
	}

	// delete from enforcer if it was registered
	if registration.Registered != nil {
		err = k.Enforcer.RemoveKnot(domain)
		if err != nil {
			l.Error("failed to update ACL", "err", err)
			fail()
			return
		}
	}

	client, err := k.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.KnotNSID,
		Repo:       user.Did,
		Rkey:       domain,
	})
	if err != nil {
		// non-fatal
		l.Error("failed to delete record", "err", err)
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to delete knot", "err", err)
		fail()
		return
	}

	err = k.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACL", "err", err)
		k.Pages.HxRefresh(w)
		return
	}

	shouldRedirect := r.Header.Get("shouldRedirect")
	if shouldRedirect == "true" {
		k.Pages.HxRedirect(w, "/knots")
		return
	}

	w.Write([]byte{})
}

func (k *Knots) retry(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	l := k.Logger.With("handler", "retry")

	noticeId := "operation-error"
	defaultErr := "Failed to verify knot. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		l.Error("empty domain")
		fail()
		return
	}
	l = l.With("domain", domain)
	l = l.With("user", user.Did)

	// get record from db first
	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
	)
	if err != nil {
		l.Error("failed to get registration", "err", err)
		fail()
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		fail()
		return
	}
	registration := registrations[0]

	// begin verification
	err = serververify.RunVerification(r.Context(), domain, user.Did, k.Config.Core.Dev)
	if err != nil {
		l.Error("verification failed", "err", err)

		if errors.Is(err, xrpcclient.ErrXrpcUnsupported) {
			k.Pages.Notice(w, noticeId, "Failed to verify knot, XRPC queries are unsupported on this knot, consider upgrading!")
			return
		}

		if e, ok := err.(*serververify.OwnerMismatch); ok {
			k.Pages.Notice(w, noticeId, e.Error())
			return
		}

		fail()
		return
	}

	err = serververify.MarkKnotVerified(k.Db, k.Enforcer, domain, user.Did)
	if err != nil {
		l.Error("failed to mark verified", "err", err)
		k.Pages.Notice(w, noticeId, err.Error())
		return
	}

	// if this knot requires upgrade, then emit a record too
	//
	// this is part of migrating from the old knot system to the new one
	if registration.NeedsUpgrade {
		// re-announce by registering under same rkey
		client, err := k.OAuth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to authorize client", "err", err)
			fail()
			return
		}

		ex, _ := client.RepoGetRecord(r.Context(), "", tangled.KnotNSID, user.Did, domain)
		var exCid *string
		if ex != nil {
			exCid = ex.Cid
		}

		// ignore the error here
		_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.KnotNSID,
			Repo:       user.Did,
			Rkey:       domain,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Knot{
					CreatedAt: time.Now().Format(time.RFC3339),
				},
			},
			SwapRecord: exCid,
		})
		if err != nil {
			l.Error("non-fatal: failed to reannouce knot", "err", err)
		}
	}

	// add this knot to knotstream
	go k.Knotstream.AddSource(
		r.Context(),
		eventconsumer.NewKnotSource(domain),
	)

	shouldRefresh := r.Header.Get("shouldRefresh")
	if shouldRefresh == "true" {
		k.Pages.HxRefresh(w)
		return
	}

	// Get updated registration to show
	registrations, err = db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
	)
	if err != nil {
		l.Error("failed to get registration", "err", err)
		fail()
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		fail()
		return
	}
	updatedRegistration := registrations[0]

	w.Header().Set("HX-Reswap", "outerHTML")
	k.Pages.KnotListing(w, pages.KnotListingParams{
		Registration: &updatedRegistration,
	})
}

func (k *Knots) addMember(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	l := k.Logger.With("handler", "addMember")

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		l.Error("empty domain")
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	l = l.With("domain", domain)
	l = l.With("user", user.Did)

	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
		db.FilterIsNot("registered", "null"),
	)
	if err != nil {
		l.Error("failed to get registration", "err", err)
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		return
	}
	registration := registrations[0]

	noticeId := fmt.Sprintf("add-member-error-%d", registration.Id)
	defaultErr := "Failed to add member. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	member := r.FormValue("member")
	if member == "" {
		l.Error("empty member")
		k.Pages.Notice(w, noticeId, "Failed to add member, empty form.")
		return
	}
	l = l.With("member", member)

	memberId, err := k.IdResolver.ResolveIdent(r.Context(), member)
	if err != nil {
		l.Error("failed to resolve member identity to handle", "err", err)
		k.Pages.Notice(w, noticeId, "Failed to add member, identity resolution failed.")
		return
	}
	if memberId.Handle.IsInvalidHandle() {
		l.Error("failed to resolve member identity to handle")
		k.Pages.Notice(w, noticeId, "Failed to add member, identity resolution failed.")
		return
	}

	// write to pds
	client, err := k.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	rkey := tid.TID()

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.KnotMemberNSID,
		Repo:       user.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.KnotMember{
				CreatedAt: time.Now().Format(time.RFC3339),
				Domain:    domain,
				Subject:   memberId.DID.String(),
			},
		},
	})
	if err != nil {
		l.Error("failed to add record to PDS", "err", err)
		k.Pages.Notice(w, noticeId, "Failed to add record to PDS, try again later.")
		return
	}

	err = k.Enforcer.AddKnotMember(domain, memberId.DID.String())
	if err != nil {
		l.Error("failed to add member to ACLs", "err", err)
		fail()
		return
	}

	err = k.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to save ACL policy", "err", err)
		fail()
		return
	}

	// success
	k.Pages.HxRedirect(w, fmt.Sprintf("/knots/%s", domain))
}

func (k *Knots) removeMember(w http.ResponseWriter, r *http.Request) {
	user := k.OAuth.GetUser(r)
	l := k.Logger.With("handler", "removeMember")

	noticeId := "operation-error"
	defaultErr := "Failed to remove member. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		l.Error("empty domain")
		fail()
		return
	}
	l = l.With("domain", domain)
	l = l.With("user", user.Did)

	registrations, err := db.GetRegistrations(
		k.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("domain", domain),
		db.FilterIsNot("registered", "null"),
	)
	if err != nil {
		l.Error("failed to get registration", "err", err)
		return
	}
	if len(registrations) != 1 {
		l.Error("got incorret number of registrations", "got", len(registrations), "expected", 1)
		return
	}

	member := r.FormValue("member")
	if member == "" {
		l.Error("empty member")
		k.Pages.Notice(w, noticeId, "Failed to remove member, empty form.")
		return
	}
	l = l.With("member", member)

	memberId, err := k.IdResolver.ResolveIdent(r.Context(), member)
	if err != nil {
		l.Error("failed to resolve member identity to handle", "err", err)
		k.Pages.Notice(w, noticeId, "Failed to remove member, identity resolution failed.")
		return
	}
	if memberId.Handle.IsInvalidHandle() {
		l.Error("failed to resolve member identity to handle")
		k.Pages.Notice(w, noticeId, "Failed to remove member, identity resolution failed.")
		return
	}

	// remove from enforcer
	err = k.Enforcer.RemoveKnotMember(domain, memberId.DID.String())
	if err != nil {
		l.Error("failed to update ACLs", "err", err)
		fail()
		return
	}

	client, err := k.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to authorize client", "err", err)
		fail()
		return
	}

	// TODO: We need to track the rkey for knot members to delete the record
	// For now, just remove from ACLs
	_ = client

	// commit everything
	err = k.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to save ACLs", "err", err)
		fail()
		return
	}

	// ok
	k.Pages.HxRefresh(w)
}
