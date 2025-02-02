package state

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/gliderlabs/ssh"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
)

type State struct {
	db       *db.DB
	auth     *auth.Auth
	enforcer *Enforcer
}

func Make() (*State, error) {
	db, err := db.Make("appview.db")
	if err != nil {
		return nil, err
	}

	auth, err := auth.Make()
	if err != nil {
		return nil, err
	}

	enforcer, err := NewEnforcer()
	if err != nil {
		return nil, err
	}

	return &State{db, auth, enforcer}, nil
}

func (s *State) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		pages.Login(w, pages.LoginParams{})
		return
	case http.MethodPost:
		handle := r.FormValue("handle")
		appPassword := r.FormValue("app_password")

		fmt.Println("handle", handle)
		fmt.Println("app_password", appPassword)

		resolved, err := auth.ResolveIdent(ctx, handle)
		if err != nil {
			log.Printf("resolving identity: %s", err)
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
	pages.Timeline(w, pages.TimelineParams{
		User: user,
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

func (s *State) Settings(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.auth.GetUser(r)
	pubKeys, err := s.db.GetPublicKeys(user.Did)
	if err != nil {
		log.Println(err)
	}

	pages.Settings(w, pages.SettingsParams{
		User:    user,
		PubKeys: pubKeys,
	})
}

func (s *State) Keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Write([]byte("unimplemented"))
		log.Println("unimplemented")
		return
	case http.MethodPut:
		did := s.auth.GetDID(r)
		key := r.FormValue("key")
		name := r.FormValue("name")
		client, _ := s.auth.AuthorizedClient(r)

		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			log.Printf("parsing public key: %s", err)
			return
		}

		if err := s.db.AddPublicKey(did, name, key); err != nil {
			log.Printf("adding public key: %s", err)
			return
		}

		// store in pds too
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.PublicKeyNSID,
			Repo:       did,
			Rkey:       uuid.New().String(),
			Record: &lexutil.LexiconTypeDecoder{Val: &tangled.PublicKey{
				Created: time.Now().String(),
				Key:     key,
				Name:    name,
			}},
		})

		// invalid record
		if err != nil {
			log.Printf("failed to create record: %s", err)
			return
		}

		log.Println("created atproto record: ", resp.Uri)

		return
	}
}

// create a signed request and check if a node responds to that
func (s *State) InitKnotServer(w http.ResponseWriter, r *http.Request) {
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
	log.Println("has secret ", secret)

	// make a request do the knotserver with an empty body and above signature
	url := fmt.Sprintf("http://%s/health", domain)

	pingRequest, err := buildPingRequest(url, secret)
	if err != nil {
		log.Println("failed to build ping request", err)
		return
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Do(pingRequest)
	if err != nil {
		w.Write([]byte("no dice"))
		log.Println("domain was unreachable after 5 seconds")
		return
	}

	if resp.StatusCode != http.StatusOK {
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

// get knots registered by this user
func (s *State) Knots(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.auth.GetUser(r)
	registrations, err := s.db.RegistrationsByDid(user.Did)
	if err != nil {
		log.Println(err)
	}

	pages.Knots(w, pages.KnotsParams{
		User:          user,
		Registrations: registrations,
	})
}

func buildPingRequest(url, secret string) (*http.Request, error) {
	pingRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(secret))
	message := pingRequest.Method + pingRequest.URL.Path + timestamp
	mac.Write([]byte(message))
	signature := hex.EncodeToString(mac.Sum(nil))

	pingRequest.Header.Set("X-Signature", signature)
	pingRequest.Header.Set("X-Timestamp", timestamp)

	return pingRequest, nil
}

func (s *State) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/", s.Timeline)

	r.Get("/login", s.Login)
	r.Post("/login", s.Login)

	r.Route("/knots", func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Get("/", s.Knots)
		r.Post("/init/{domain}", s.InitKnotServer)
		r.Post("/key", s.RegistrationKey)
	})

	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Get("/settings", s.Settings)
		r.Put("/settings/keys", s.Keys)
	})

	return r
}
