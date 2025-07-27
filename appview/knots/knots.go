package knots

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/knotclient"
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

func (k *Knots) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.AuthMiddleware(k.OAuth))

	r.Get("/", k.index)
	r.Post("/key", k.generateKey)

	r.Route("/{domain}", func(r chi.Router) {
		r.Post("/init", k.init)
		r.Get("/", k.dashboard)
		r.Route("/member", func(r chi.Router) {
			r.Use(mw.KnotOwner())
			r.Get("/", k.members)
			r.Put("/", k.addMember)
			r.Delete("/", k.removeMember)
		})
	})

	return r
}

// get knots registered by this user
func (k *Knots) index(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "index")

	user := k.OAuth.GetUser(r)
	registrations, err := db.RegistrationsByDid(k.Db, user.Did)
	if err != nil {
		l.Error("failed to get registrations by did", "err", err)
	}

	k.Pages.Knots(w, pages.KnotsParams{
		LoggedInUser:  user,
		Registrations: registrations,
	})
}

// requires auth
func (k *Knots) generateKey(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "generateKey")

	user := k.OAuth.GetUser(r)
	did := user.Did
	l = l.With("did", did)

	// check if domain is valid url, and strip extra bits down to just host
	domain := r.FormValue("domain")
	if domain == "" {
		l.Error("empty domain")
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	l = l.With("domain", domain)

	noticeId := "registration-error"
	fail := func() {
		k.Pages.Notice(w, noticeId, "Failed to generate registration key.")
	}

	key, err := db.GenerateRegistrationKey(k.Db, domain, did)
	if err != nil {
		l.Error("failed to generate registration key", "err", err)
		fail()
		return
	}

	allRegs, err := db.RegistrationsByDid(k.Db, did)
	if err != nil {
		l.Error("failed to generate registration key", "err", err)
		fail()
		return
	}

	k.Pages.KnotListingFull(w, pages.KnotListingFullParams{
		Registrations: allRegs,
	})
	k.Pages.KnotSecret(w, pages.KnotSecretParams{
		Secret: key,
	})
}

// create a signed request and check if a node responds to that
func (k *Knots) init(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "init")
	user := k.OAuth.GetUser(r)

	noticeId := "operation-error"
	defaultErr := "Failed to initialize knot. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	l = l.With("domain", domain)

	l.Info("checking domain")

	registration, err := db.RegistrationByDomain(k.Db, domain)
	if err != nil {
		l.Error("failed to get registration for domain", "err", err)
		fail()
		return
	}
	if registration.ByDid != user.Did {
		l.Error("unauthorized", "wantedDid", registration.ByDid, "gotDid", user.Did)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	secret, err := db.GetRegistrationKey(k.Db, domain)
	if err != nil {
		l.Error("failed to get registration key for domain", "err", err)
		fail()
		return
	}

	client, err := knotclient.NewSignedClient(domain, secret, k.Config.Core.Dev)
	if err != nil {
		l.Error("failed to create knotclient", "err", err)
		fail()
		return
	}

	resp, err := client.Init(user.Did)
	if err != nil {
		k.Pages.Notice(w, noticeId, fmt.Sprintf("Failed to make request: %s", err.Error()))
		l.Error("failed to make init request", "err", err)
		return
	}

	if resp.StatusCode == http.StatusConflict {
		k.Pages.Notice(w, noticeId, "This knot is already registered")
		l.Error("knot already registered", "statuscode", resp.StatusCode)
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		k.Pages.Notice(w, noticeId, fmt.Sprintf("Received status %d from knot, expected %d", resp.StatusCode, http.StatusNoContent))
		l.Error("incorrect statuscode returned", "statuscode", resp.StatusCode, "expected", http.StatusNoContent)
		return
	}

	// verify response mac
	signature := resp.Header.Get("X-Signature")
	signatureBytes, err := hex.DecodeString(signature)
	if err != nil {
		return
	}

	expectedMac := hmac.New(sha256.New, []byte(secret))
	expectedMac.Write([]byte("ok"))

	if !hmac.Equal(expectedMac.Sum(nil), signatureBytes) {
		k.Pages.Notice(w, noticeId, "Response signature mismatch, consider regenerating the secret and retrying.")
		l.Error("signature mismatch", "bytes", signatureBytes)
		return
	}

	tx, err := k.Db.BeginTx(r.Context(), nil)
	if err != nil {
		l.Error("failed to start tx", "err", err)
		fail()
		return
	}
	defer func() {
		tx.Rollback()
		err = k.Enforcer.E.LoadPolicy()
		if err != nil {
			l.Error("rollback failed", "err", err)
		}
	}()

	// mark as registered
	err = db.Register(tx, domain)
	if err != nil {
		l.Error("failed to register domain", "err", err)
		fail()
		return
	}

	// set permissions for this did as owner
	reg, err := db.RegistrationByDomain(tx, domain)
	if err != nil {
		l.Error("failed get registration by domain", "err", err)
		fail()
		return
	}

	// add basic acls for this domain
	err = k.Enforcer.AddKnot(domain)
	if err != nil {
		l.Error("failed to add knot to enforcer", "err", err)
		fail()
		return
	}

	// add this did as owner of this domain
	err = k.Enforcer.AddKnotOwner(domain, reg.ByDid)
	if err != nil {
		l.Error("failed to add knot owner to enforcer", "err", err)
		fail()
		return
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit changes", "err", err)
		fail()
		return
	}

	err = k.Enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACLs", "err", err)
		fail()
		return
	}

	// add this knot to knotstream
	go k.Knotstream.AddSource(
		context.Background(),
		eventconsumer.NewKnotSource(domain),
	)

	k.Pages.KnotListing(w, pages.KnotListingParams{
		Registration: *reg,
	})
}

func (k *Knots) dashboard(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "dashboard")
	fail := func() {
		w.WriteHeader(http.StatusInternalServerError)
	}

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	l = l.With("domain", domain)

	user := k.OAuth.GetUser(r)
	l = l.With("did", user.Did)

	// dashboard is only available to owners
	ok, err := k.Enforcer.IsKnotOwner(user.Did, domain)
	if err != nil {
		l.Error("failed to query enforcer", "err", err)
		fail()
	}
	if !ok {
		http.Error(w, "only owners can view dashboards", http.StatusUnauthorized)
		return
	}

	reg, err := db.RegistrationByDomain(k.Db, domain)
	if err != nil {
		l.Error("failed to get registration by domain", "err", err)
		fail()
		return
	}

	var members []string
	if reg.Registered != nil {
		members, err = k.Enforcer.GetUserByRole("server:member", domain)
		if err != nil {
			l.Error("failed to get members list", "err", err)
			fail()
			return
		}
	}

	repos, err := db.GetRepos(
		k.Db,
		0,
		db.FilterEq("knot", domain),
		db.FilterIn("did", members),
	)
	if err != nil {
		l.Error("failed to get repos list", "err", err)
		fail()
		return
	}
	// convert to map
	repoByMember := make(map[string][]db.Repo)
	for _, r := range repos {
		repoByMember[r.Did] = append(repoByMember[r.Did], r)
	}

	var didsToResolve []string
	for _, m := range members {
		didsToResolve = append(didsToResolve, m)
	}
	didsToResolve = append(didsToResolve, reg.ByDid)
	resolvedIds := k.IdResolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	k.Pages.Knot(w, pages.KnotParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		Registration: reg,
		Members:      members,
		Repos:        repoByMember,
		IsOwner:      true,
	})
}

// list members of domain, requires auth and requires owner status
func (k *Knots) members(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "members")

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	l = l.With("domain", domain)

	// list all members for this domain
	memberDids, err := k.Enforcer.GetUserByRole("server:member", domain)
	if err != nil {
		w.Write([]byte("failed to fetch member list"))
		return
	}

	w.Write([]byte(strings.Join(memberDids, "\n")))
	return
}

// add member to domain, requires auth and requires invite access
func (k *Knots) addMember(w http.ResponseWriter, r *http.Request) {
	l := k.Logger.With("handler", "members")

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	l = l.With("domain", domain)

	reg, err := db.RegistrationByDomain(k.Db, domain)
	if err != nil {
		l.Error("failed to get registration by domain", "err", err)
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}

	noticeId := fmt.Sprintf("add-member-error-%d", reg.Id)
	l = l.With("notice-id", noticeId)
	defaultErr := "Failed to add member. Try again later."
	fail := func() {
		k.Pages.Notice(w, noticeId, defaultErr)
	}

	subjectIdentifier := r.FormValue("subject")
	if subjectIdentifier == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	l = l.With("subjectIdentifier", subjectIdentifier)

	subjectIdentity, err := k.IdResolver.ResolveIdent(r.Context(), subjectIdentifier)
	if err != nil {
		l.Error("failed to resolve identity", "err", err)
		k.Pages.Notice(w, noticeId, "Failed to add member, identity resolution failed.")
		return
	}
	l = l.With("subjectDid", subjectIdentity.DID)

	l.Info("adding member to knot")

	// announce this relation into the firehose, store into owners' pds
	client, err := k.OAuth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to create client", "err", err)
		fail()
		return
	}

	currentUser := k.OAuth.GetUser(r)
	createdAt := time.Now().Format(time.RFC3339)
	resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.KnotMemberNSID,
		Repo:       currentUser.Did,
		Rkey:       tid.TID(),
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.KnotMember{
				Subject:   subjectIdentity.DID.String(),
				Domain:    domain,
				CreatedAt: createdAt,
			}},
	})
	// invalid record
	if err != nil {
		l.Error("failed to write to PDS", "err", err)
		fail()
		return
	}
	l = l.With("at-uri", resp.Uri)
	l.Info("wrote record to PDS")

	secret, err := db.GetRegistrationKey(k.Db, domain)
	if err != nil {
		l.Error("failed to get registration key", "err", err)
		fail()
		return
	}

	ksClient, err := knotclient.NewSignedClient(domain, secret, k.Config.Core.Dev)
	if err != nil {
		l.Error("failed to create client", "err", err)
		fail()
		return
	}

	ksResp, err := ksClient.AddMember(subjectIdentity.DID.String())
	if err != nil {
		l.Error("failed to reach knotserver", "err", err)
		k.Pages.Notice(w, noticeId, "Failed to reach to knotserver.")
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		l.Error("status mismatch", "got", ksResp.StatusCode, "expected", http.StatusNoContent)
		k.Pages.Notice(w, noticeId, fmt.Sprintf("Unexpected status code from knotserver %d, expected %d", ksResp.StatusCode, http.StatusNoContent))
		return
	}

	err = k.Enforcer.AddKnotMember(domain, subjectIdentity.DID.String())
	if err != nil {
		l.Error("failed to add member to enforcer", "err", err)
		fail()
		return
	}

	// success
	k.Pages.HxRedirect(w, fmt.Sprintf("/knots/%s", domain))
}

func (k *Knots) removeMember(w http.ResponseWriter, r *http.Request) {
}
