package state

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/posthog/posthog-go"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/cache"
	"tangled.sh/tangled.sh/core/appview/cache/session"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/rbac"
)

type State struct {
	db           *db.DB
	oauth        *oauth.OAuth
	enforcer     *rbac.Enforcer
	tidClock     syntax.TIDClock
	pages        *pages.Pages
	sess         *session.SessionStore
	idResolver   *idresolver.Resolver
	posthog      posthog.Client
	jc           *jetstream.JetstreamClient
	config       *config.Config
	repoResolver *reporesolver.RepoResolver
	knotstream   *knotclient.EventConsumer
}

func Make(ctx context.Context, config *config.Config) (*State, error) {
	d, err := db.Make(config.Core.DbPath)
	if err != nil {
		return nil, err
	}

	enforcer, err := rbac.NewEnforcer(config.Core.DbPath)
	if err != nil {
		return nil, err
	}

	clock := syntax.NewTIDClock(0)

	pgs := pages.NewPages(config)

	res, err := idresolver.RedisResolver(config.Redis)
	if err != nil {
		log.Printf("failed to create redis resolver: %v", err)
		res = idresolver.DefaultResolver()
	}

	cache := cache.New(config.Redis.Addr)
	sess := session.New(cache)

	oauth := oauth.NewOAuth(config, sess)

	posthog, err := posthog.NewWithConfig(config.Posthog.ApiKey, posthog.Config{Endpoint: config.Posthog.Endpoint})
	if err != nil {
		return nil, fmt.Errorf("failed to create posthog client: %w", err)
	}

	repoResolver := reporesolver.New(config, enforcer, res, d)

	wrapper := db.DbWrapper{d}
	jc, err := jetstream.NewJetstreamClient(
		config.Jetstream.Endpoint,
		"appview",
		[]string{
			tangled.GraphFollowNSID,
			tangled.FeedStarNSID,
			tangled.PublicKeyNSID,
			tangled.RepoArtifactNSID,
			tangled.ActorProfileNSID,
		},
		nil,
		slog.Default(),
		wrapper,
		false,

		// in-memory filter is inapplicalble to appview so
		// we'll never log dids anyway.
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create jetstream client: %w", err)
	}
	err = jc.StartJetstream(ctx, appview.Ingest(wrapper, enforcer))
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream watcher: %w", err)
	}

	knotstream, err := KnotstreamConsumer(ctx, config, d, enforcer, posthog)
	if err != nil {
		return nil, fmt.Errorf("failed to start knotstream consumer: %w", err)
	}
	knotstream.Start(ctx)

	state := &State{
		d,
		oauth,
		enforcer,
		clock,
		pgs,
		sess,
		res,
		posthog,
		jc,
		config,
		repoResolver,
		knotstream,
	}

	return state, nil
}

func TID(c *syntax.TIDClock) string {
	return c.Next().String()
}

func (s *State) Timeline(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	timeline, err := db.MakeTimeline(s.db)
	if err != nil {
		log.Println(err)
		s.pages.Notice(w, "timeline", "Uh oh! Failed to load timeline.")
	}

	var didsToResolve []string
	for _, ev := range timeline {
		if ev.Repo != nil {
			didsToResolve = append(didsToResolve, ev.Repo.Did)
			if ev.Source != nil {
				didsToResolve = append(didsToResolve, ev.Source.Did)
			}
		}
		if ev.Follow != nil {
			didsToResolve = append(didsToResolve, ev.Follow.UserDid, ev.Follow.SubjectDid)
		}
		if ev.Star != nil {
			didsToResolve = append(didsToResolve, ev.Star.StarredByDid, ev.Star.Repo.Did)
		}
	}

	resolvedIds := s.idResolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	s.pages.Timeline(w, pages.TimelineParams{
		LoggedInUser: user,
		Timeline:     timeline,
		DidHandleMap: didHandleMap,
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
		session, err := s.oauth.Stores().Get(r, oauth.SessionName)
		if err != nil || session.IsNew {
			log.Println("unauthorized attempt to generate registration key")
			http.Error(w, "Forbidden", http.StatusUnauthorized)
			return
		}

		did := session.Values[oauth.SessionDid].(string)

		// check if domain is valid url, and strip extra bits down to just host
		domain := r.FormValue("domain")
		if domain == "" {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}

		key, err := db.GenerateRegistrationKey(s.db, domain, did)

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

	id, err := s.idResolver.ResolveIdent(r.Context(), user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pubKeys, err := db.GetPublicKeysForDid(s.db, id.DID.String())
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
	user := s.oauth.GetUser(r)

	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}
	log.Println("checking ", domain)

	secret, err := db.GetRegistrationKey(s.db, domain)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", domain, err)
		return
	}

	client, err := knotclient.NewSignedClient(domain, secret, s.config.Core.Dev)
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

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		tx.Rollback()
		err = s.enforcer.E.LoadPolicy()
		if err != nil {
			log.Println("failed to rollback policies")
		}
	}()

	// mark as registered
	err = db.Register(tx, domain)
	if err != nil {
		log.Println("failed to register domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// set permissions for this did as owner
	reg, err := db.RegistrationByDomain(tx, domain)
	if err != nil {
		log.Println("failed to register domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add basic acls for this domain
	err = s.enforcer.AddKnot(domain)
	if err != nil {
		log.Println("failed to setup owner of domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add this did as owner of this domain
	err = s.enforcer.AddKnotOwner(domain, reg.ByDid)
	if err != nil {
		log.Println("failed to setup owner of domain", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit changes", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = s.enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to update ACLs", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add this knot to knotstream
	go s.knotstream.AddSource(context.Background(), knotclient.EventSource{domain})

	w.Write([]byte("check success"))
}

func (s *State) KnotServerInfo(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		http.Error(w, "malformed url", http.StatusBadRequest)
		return
	}

	user := s.oauth.GetUser(r)
	reg, err := db.RegistrationByDomain(s.db, domain)
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

	var didsToResolve []string
	for _, m := range members {
		didsToResolve = append(didsToResolve, m)
	}
	didsToResolve = append(didsToResolve, reg.ByDid)
	resolvedIds := s.idResolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	ok, err := s.enforcer.IsKnotOwner(user.Did, domain)
	isOwner := err == nil && ok

	p := pages.KnotParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		Registration: reg,
		Members:      members,
		IsOwner:      isOwner,
	}

	s.pages.Knot(w, p)
}

// get knots registered by this user
func (s *State) Knots(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.oauth.GetUser(r)
	registrations, err := db.RegistrationsByDid(s.db, user.Did)
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

	subjectIdentifier := r.FormValue("subject")
	if subjectIdentifier == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	subjectIdentity, err := s.idResolver.ResolveIdent(r.Context(), subjectIdentifier)
	if err != nil {
		w.Write([]byte("failed to resolve member did to a handle"))
		return
	}
	log.Printf("adding %s to %s\n", subjectIdentity.Handle.String(), domain)

	// announce this relation into the firehose, store into owners' pds
	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		http.Error(w, "failed to authorize client", http.StatusInternalServerError)
		return
	}
	currentUser := s.oauth.GetUser(r)
	createdAt := time.Now().Format(time.RFC3339)
	resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.KnotMemberNSID,
		Repo:       currentUser.Did,
		Rkey:       appview.TID(),
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.KnotMember{
				Subject:   subjectIdentity.DID.String(),
				Domain:    domain,
				CreatedAt: createdAt,
			}},
	})

	// invalid record
	if err != nil {
		log.Printf("failed to create record: %s", err)
		return
	}
	log.Println("created atproto record: ", resp.Uri)

	secret, err := db.GetRegistrationKey(s.db, domain)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", domain, err)
		return
	}

	ksClient, err := knotclient.NewSignedClient(domain, secret, s.config.Core.Dev)
	if err != nil {
		log.Println("failed to create client to ", domain)
		return
	}

	ksResp, err := ksClient.AddMember(subjectIdentity.DID.String())
	if err != nil {
		log.Printf("failed to make request to %s: %s", domain, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		w.Write([]byte(fmt.Sprint("knotserver failed to add member: ", err)))
		return
	}

	err = s.enforcer.AddKnotMember(domain, subjectIdentity.DID.String())
	if err != nil {
		w.Write([]byte(fmt.Sprint("failed to add member: ", err)))
		return
	}

	w.Write([]byte(fmt.Sprint("added member: ", subjectIdentity.Handle.String())))
}

func (s *State) RemoveMember(w http.ResponseWriter, r *http.Request) {
}

func validateRepoName(name string) error {
	// check for path traversal attempts
	if name == "." || name == ".." ||
		strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("Repository name contains invalid path characters")
	}

	// check for sequences that could be used for traversal when normalized
	if strings.Contains(name, "./") || strings.Contains(name, "../") ||
		strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("Repository name contains invalid path sequence")
	}

	// then continue with character validation
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.') {
			return fmt.Errorf("Repository name can only contain alphanumeric characters, periods, hyphens, and underscores")
		}
	}

	// additional check to prevent multiple sequential dots
	if strings.Contains(name, "..") {
		return fmt.Errorf("Repository name cannot contain sequential dots")
	}

	// if all checks pass
	return nil
}

func (s *State) NewRepo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := s.oauth.GetUser(r)
		knots, err := s.enforcer.GetKnotsForUser(user.Did)
		if err != nil {
			s.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		s.pages.NewRepo(w, pages.NewRepoParams{
			LoggedInUser: user,
			Knots:        knots,
		})

	case http.MethodPost:
		user := s.oauth.GetUser(r)

		domain := r.FormValue("domain")
		if domain == "" {
			s.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}

		repoName := r.FormValue("name")
		if repoName == "" {
			s.pages.Notice(w, "repo", "Repository name cannot be empty.")
			return
		}

		if err := validateRepoName(repoName); err != nil {
			s.pages.Notice(w, "repo", err.Error())
			return
		}

		defaultBranch := r.FormValue("branch")
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		description := r.FormValue("description")

		ok, err := s.enforcer.E.Enforce(user.Did, domain, domain, "repo:create")
		if err != nil || !ok {
			s.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		existingRepo, err := db.GetRepo(s.db, user.Did, repoName)
		if err == nil && existingRepo != nil {
			s.pages.Notice(w, "repo", fmt.Sprintf("A repo by this name already exists on %s", existingRepo.Knot))
			return
		}

		secret, err := db.GetRegistrationKey(s.db, domain)
		if err != nil {
			s.pages.Notice(w, "repo", fmt.Sprintf("No registration key found for knot %s.", domain))
			return
		}

		client, err := knotclient.NewSignedClient(domain, secret, s.config.Core.Dev)
		if err != nil {
			s.pages.Notice(w, "repo", "Failed to connect to knot server.")
			return
		}

		rkey := appview.TID()
		repo := &db.Repo{
			Did:         user.Did,
			Name:        repoName,
			Knot:        domain,
			Rkey:        rkey,
			Description: description,
		}

		xrpcClient, err := s.oauth.AuthorizedClient(r)
		if err != nil {
			s.pages.Notice(w, "repo", "Failed to write record to PDS.")
			return
		}

		createdAt := time.Now().Format(time.RFC3339)
		atresp, err := xrpcClient.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Repo{
					Knot:      repo.Knot,
					Name:      repoName,
					CreatedAt: createdAt,
					Owner:     user.Did,
				}},
		})
		if err != nil {
			log.Printf("failed to create record: %s", err)
			s.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}
		log.Println("created repo record: ", atresp.Uri)

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}
		defer func() {
			tx.Rollback()
			err = s.enforcer.E.LoadPolicy()
			if err != nil {
				log.Println("failed to rollback policies")
			}
		}()

		resp, err := client.NewRepo(user.Did, repoName, defaultBranch)
		if err != nil {
			s.pages.Notice(w, "repo", "Failed to create repository on knot server.")
			return
		}

		switch resp.StatusCode {
		case http.StatusConflict:
			s.pages.Notice(w, "repo", "A repository with that name already exists.")
			return
		case http.StatusInternalServerError:
			s.pages.Notice(w, "repo", "Failed to create repository on knot. Try again later.")
		case http.StatusNoContent:
			// continue
		}

		repo.AtUri = atresp.Uri
		err = db.AddRepo(tx, repo)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, repoName)
		err = s.enforcer.AddRepo(user.Did, domain, p)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			log.Println("failed to commit changes", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = s.enforcer.E.SavePolicy()
		if err != nil {
			log.Println("failed to update ACLs", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: user.Did,
				Event:      "new_repo",
				Properties: posthog.Properties{"repo": repoName, "repo_at": repo.AtUri},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Handle, repoName))
		return
	}
}
