package state

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
	"github.com/sotangled/tangled/rbac"
)

type State struct {
	db       *db.DB
	auth     *auth.Auth
	enforcer *rbac.Enforcer
	tidClock *syntax.TIDClock
	pages    *pages.Pages
	resolver *appview.Resolver
}

func Make() (*State, error) {
	db, err := db.Make(appview.SqliteDbPath)
	if err != nil {
		return nil, err
	}

	auth, err := auth.Make()
	if err != nil {
		return nil, err
	}

	enforcer, err := rbac.NewEnforcer(appview.SqliteDbPath)
	if err != nil {
		return nil, err
	}

	clock := syntax.NewTIDClock(0)

	pgs := pages.NewPages()

	resolver := appview.NewResolver()

	state := &State{
		db,
		auth, enforcer, clock, pgs, resolver,
	}

	return state, nil
}

func (s *State) TID() string {
	return s.tidClock.Next().String()
}

func (s *State) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		err := s.pages.Login(w, pages.LoginParams{})
		if err != nil {
			log.Printf("rendering login page: %s", err)
		}
		return
	case http.MethodPost:
		handle := r.FormValue("handle")
		appPassword := r.FormValue("app_password")

		fmt.Println("handle", handle)
		fmt.Println("app_password", appPassword)

		resolved, err := s.resolver.ResolveIdent(ctx, handle)
		if err != nil {
			log.Printf("resolving identity: %s", err)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		atSession, err := s.auth.CreateInitialSession(ctx, resolved, appPassword)
		if err != nil {
			log.Printf("creating initial session: %s", err)
			return
		}
		sessionish := auth.CreateSessionWrapper{ServerCreateSession_Output: atSession}

		err = s.auth.StoreSession(r, w, &sessionish, resolved.PDSEndpoint())
		if err != nil {
			log.Printf("storing session: %s", err)
			return
		}

		log.Printf("successfully saved session for %s (%s)", atSession.Handle, atSession.Did)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
}

func (s *State) Timeline(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	s.pages.Timeline(w, pages.TimelineParams{
		LoggedInUser: user,
	})
	return
}

// requires auth
func (s *State) RegistrationKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// list open registrations under this did

		return
	case http.MethodPost:
		session, err := s.auth.Store.Get(r, appview.SessionName)
		if err != nil || session.IsNew {
			log.Println("unauthorized attempt to generate registration key")
			http.Error(w, "Forbidden", http.StatusUnauthorized)
			return
		}

		did := session.Values[appview.SessionDid].(string)

		// check if domain is valid url, and strip extra bits down to just host
		domain := r.FormValue("domain")
		if domain == "" {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}

		key, err := s.db.GenerateRegistrationKey(domain, did)

		if err != nil {
			log.Println(err)
			http.Error(w, "unable to register this domain", http.StatusNotAcceptable)
			return
		}

		w.Write([]byte(key))
	}
}

func (s *State) Keys(w http.ResponseWriter, r *http.Request) {
	user := chi.URLParam(r, "user")
	user = strings.TrimPrefix(user, "@")

	if user == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	id, err := s.resolver.ResolveIdent(r.Context(), user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pubKeys, err := s.db.GetPublicKeys(id.DID.String())
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if len(pubKeys) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	for _, k := range pubKeys {
		key := strings.TrimRight(k.Key, "\n")
		w.Write([]byte(fmt.Sprintln(key)))
	}
}

// create a signed request and check if a node responds to that
func (s *State) InitKnotServer(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	log.Println("checking ", domain)

	secret, err := s.db.GetRegistrationKey(domain)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", domain, err)
		return
	}

	client, err := NewSignedClient(domain, secret)
	if err != nil {
		log.Println("failed to create client to ", domain)
	}

	resp, err := client.Init(user.Did)
	if err != nil {
		w.Write([]byte("no dice"))
		log.Println("domain was unreachable after 5 seconds")
		return
	}

	if resp.StatusCode == http.StatusConflict {
		log.Println("status conflict", resp.StatusCode)
		w.Write([]byte("already registered, sorry!"))
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		log.Println("status nok", resp.StatusCode)
		w.Write([]byte("no dice"))
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
		log.Printf("response body signature mismatch: %x\n", signatureBytes)
		return
	}

	// mark as registered
	err = s.db.Register(domain)
	if err != nil {
		log.Println("failed to register domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// set permissions for this did as owner
	reg, err := s.db.RegistrationByDomain(domain)
	if err != nil {
		log.Println("failed to register domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add basic acls for this domain
	err = s.enforcer.AddDomain(domain)
	if err != nil {
		log.Println("failed to setup owner of domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add this did as owner of this domain
	err = s.enforcer.AddOwner(domain, reg.ByDid)
	if err != nil {
		log.Println("failed to setup owner of domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("check success"))
}

func (s *State) KnotServerInfo(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}

	user := s.auth.GetUser(r)
	reg, err := s.db.RegistrationByDomain(domain)
	if err != nil {
		w.Write([]byte("failed to pull up registration info"))
		return
	}

	var members []string
	if reg.Registered != nil {
		members, err = s.enforcer.GetUserByRole("server:member", domain)
		if err != nil {
			w.Write([]byte("failed to fetch member list"))
			return
		}
	}

	ok, err := s.enforcer.IsServerOwner(user.Did, domain)
	isOwner := err == nil && ok

	p := pages.KnotParams{
		LoggedInUser: user,
		Registration: reg,
		Members:      members,
		IsOwner:      isOwner,
	}

	s.pages.Knot(w, p)
}

// get knots registered by this user
func (s *State) Knots(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.auth.GetUser(r)
	registrations, err := s.db.RegistrationsByDid(user.Did)
	if err != nil {
		log.Println(err)
	}

	s.pages.Knots(w, pages.KnotsParams{
		LoggedInUser:  user,
		Registrations: registrations,
	})
}

// list members of domain, requires auth and requires owner status
func (s *State) ListMembers(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}

	// list all members for this domain
	memberDids, err := s.enforcer.GetUserByRole("server:member", domain)
	if err != nil {
		w.Write([]byte("failed to fetch member list"))
		return
	}

	w.Write([]byte(strings.Join(memberDids, "\n")))
	return
}

// add member to domain, requires auth and requires invite access
func (s *State) AddMember(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}

	memberDid := r.FormValue("member")
	if memberDid == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	memberIdent, err := s.resolver.ResolveIdent(r.Context(), memberDid)
	if err != nil {
		w.Write([]byte("failed to resolve member did to a handle"))
		return
	}
	log.Printf("adding %s to %s\n", memberIdent.Handle.String(), domain)

	// announce this relation into the firehose, store into owners' pds
	client, _ := s.auth.AuthorizedClient(r)
	currentUser := s.auth.GetUser(r)
	addedAt := time.Now().Format(time.RFC3339)
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.KnotMemberNSID,
		Repo:       currentUser.Did,
		Rkey:       s.TID(),
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.KnotMember{
				Member:  memberIdent.DID.String(),
				Domain:  domain,
				AddedAt: &addedAt,
			}},
	})
	// invalid record
	if err != nil {
		log.Printf("failed to create record: %s", err)
		return
	}
	log.Println("created atproto record: ", resp.Uri)

	secret, err := s.db.GetRegistrationKey(domain)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", domain, err)
		return
	}

	ksClient, err := NewSignedClient(domain, secret)
	if err != nil {
		log.Println("failed to create client to ", domain)
		return
	}

	ksResp, err := ksClient.AddMember(memberIdent.DID.String())
	if err != nil {
		log.Printf("failed to make request to %s: %s", domain, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		w.Write([]byte(fmt.Sprint("knotserver failed to add member: ", err)))
		return
	}

	err = s.enforcer.AddMember(domain, memberIdent.DID.String())
	if err != nil {
		w.Write([]byte(fmt.Sprint("failed to add member: ", err)))
		return
	}

	w.Write([]byte(fmt.Sprint("added member: ", memberIdent.Handle.String())))
}

func (s *State) RemoveMember(w http.ResponseWriter, r *http.Request) {
}

func (s *State) AddRepo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := s.auth.GetUser(r)
		knots, err := s.enforcer.GetDomainsForUser(user.Did)

		if err != nil {
			log.Println("invalid user?", err)
			return
		}

		s.pages.NewRepo(w, pages.NewRepoParams{
			LoggedInUser: user,
			Knots:        knots,
		})
	case http.MethodPost:
		user := s.auth.GetUser(r)

		domain := r.FormValue("domain")
		if domain == "" {
			log.Println("invalid form")
			return
		}

		repoName := r.FormValue("name")
		if repoName == "" {
			log.Println("invalid form")
			return
		}

		ok, err := s.enforcer.E.Enforce(user.Did, domain, domain, "repo:create")
		if err != nil || !ok {
			w.Write([]byte("domain inaccessible to you"))
			return
		}

		secret, err := s.db.GetRegistrationKey(domain)
		if err != nil {
			log.Printf("no key found for domain %s: %s\n", domain, err)
			return
		}

		client, err := NewSignedClient(domain, secret)
		if err != nil {
			log.Println("failed to create client to ", domain)
		}

		resp, err := client.NewRepo(user.Did, repoName)
		if err != nil {
			log.Println("failed to send create repo request", err)
			return
		}
		if resp.StatusCode != http.StatusNoContent {
			log.Println("server returned ", resp.StatusCode)
			return
		}

		// add to local db
		repo := &db.Repo{
			Did:  user.Did,
			Name: repoName,
			Knot: domain,
		}
		err = s.db.AddRepo(repo)
		if err != nil {
			log.Println("failed to add repo to db", err)
			return
		}

		// acls
		err = s.enforcer.AddRepo(user.Did, domain, filepath.Join(user.Did, repoName))
		if err != nil {
			log.Println("failed to set up acls", err)
			return
		}

		w.Write([]byte("created!"))
	}
}

func (s *State) ProfilePage(w http.ResponseWriter, r *http.Request) {
	didOrHandle := chi.URLParam(r, "user")
	if didOrHandle == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ident, err := s.resolver.ResolveIdent(r.Context(), didOrHandle)
	if err != nil {
		log.Printf("resolving identity: %s", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	repos, err := s.db.GetAllReposByDid(ident.DID.String())
	if err != nil {
		log.Printf("getting repos for %s: %s", ident.DID.String(), err)
	}

	s.pages.ProfilePage(w, pages.ProfilePageParams{
		LoggedInUser: s.auth.GetUser(r),
		UserDid:      ident.DID.String(),
		UserHandle:   ident.Handle.String(),
		Repos:        repos,
	})
}

func (s *State) Follow(w http.ResponseWriter, r *http.Request) {
	subject := r.FormValue("subject")

	if subject == "" {
		log.Println("invalid form")
		return
	}

	subjectIdent, err := s.resolver.ResolveIdent(r.Context(), subject)
	currentUser := s.auth.GetUser(r)

	client, _ := s.auth.AuthorizedClient(r)
	createdAt := time.Now().Format(time.RFC3339)
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.GraphFollowNSID,
		Repo:       currentUser.Did,
		Rkey:       s.TID(),
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.GraphFollow{
				Subject:   subjectIdent.DID.String(),
				CreatedAt: createdAt,
			}},
	})

	err = s.db.AddFollow(currentUser.Did, subjectIdent.DID.String())
	if err != nil {
		log.Println("failed to follow", err)
		return
	}

	// invalid record
	if err != nil {
		log.Printf("failed to create record: %s", err)
		return
	}
	log.Println("created atproto record: ", resp.Uri)

	return
}

func (s *State) Router() http.Handler {
	router := chi.NewRouter()

	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		pat := chi.URLParam(r, "*")
		if strings.HasPrefix(pat, "did:") || strings.HasPrefix(pat, "@") {
			s.UserRouter().ServeHTTP(w, r)
		} else {
			s.StandardRouter().ServeHTTP(w, r)
		}
	})

	return router
}

func (s *State) UserRouter() http.Handler {
	r := chi.NewRouter()

	// strip @ from user
	r.Use(StripLeadingAt)

	r.With(ResolveIdent(s)).Route("/{user}", func(r chi.Router) {
		r.Get("/", s.ProfilePage)
		r.With(ResolveRepoKnot(s)).Route("/{repo}", func(r chi.Router) {
			r.Get("/", s.RepoIndex)
			r.Get("/log/{ref}", s.RepoLog)
			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/*", s.RepoTree)
			})
			r.Get("/commit/{ref}", s.RepoCommit)
			r.Get("/branches", s.RepoBranches)
			r.Get("/tags", s.RepoTags)
			r.Get("/blob/{ref}/*", s.RepoBlob)

			// These routes get proxied to the knot
			r.Get("/info/refs", s.InfoRefs)
			r.Post("/git-upload-pack", s.UploadPack)

		})
	})

	return r
}

func (s *State) StandardRouter() http.Handler {
	r := chi.NewRouter()

	r.Get("/", s.Timeline)

	r.Get("/login", s.Login)
	r.Post("/login", s.Login)

	r.Route("/knots", func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Get("/", s.Knots)
		r.Post("/key", s.RegistrationKey)

		r.Route("/{domain}", func(r chi.Router) {
			r.Post("/init", s.InitKnotServer)
			r.Get("/", s.KnotServerInfo)
			r.Route("/member", func(r chi.Router) {
				r.Use(RoleMiddleware(s, "server:owner"))
				r.Get("/", s.ListMembers)
				r.Put("/", s.AddMember)
				r.Delete("/", s.RemoveMember)
			})
		})
	})

	r.Route("/repo", func(r chi.Router) {
		r.Route("/new", func(r chi.Router) {
			r.Get("/", s.AddRepo)
			r.Post("/", s.AddRepo)
		})
		// r.Post("/import", s.ImportRepo)
	})

	r.With(AuthMiddleware(s)).Put("/follow", s.Follow)

	r.Route("/settings", func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Get("/", s.Settings)
		r.Put("/keys", s.SettingsKeys)
	})

	r.Get("/keys/{user}", s.Keys)

	return r
}
