package oauth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	atpclient "github.com/bluesky-social/indigo/atproto/client"
	atcrypto "github.com/bluesky-social/indigo/atproto/crypto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	xrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/gorilla/sessions"
	"github.com/posthog/posthog-go"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/idresolver"
	"tangled.org/core/rbac"
)

type OAuth struct {
	ClientApp  *oauth.ClientApp
	SessStore  *sessions.CookieStore
	Config     *config.Config
	JwksUri    string
	ClientName string
	ClientUri  string
	Posthog    posthog.Client
	Db         *db.DB
	Enforcer   *rbac.Enforcer
	IdResolver *idresolver.Resolver
	Logger     *slog.Logger
}

func New(config *config.Config, ph posthog.Client, db *db.DB, enforcer *rbac.Enforcer, res *idresolver.Resolver, logger *slog.Logger) (*OAuth, error) {
	var oauthConfig oauth.ClientConfig
	var clientUri string
	if config.Core.Dev {
		clientUri = "http://127.0.0.1:3000"
		callbackUri := clientUri + "/oauth/callback"
		oauthConfig = oauth.NewLocalhostConfig(callbackUri, []string{"atproto", "transition:generic"})
	} else {
		clientUri = config.Core.AppviewHost
		clientId := fmt.Sprintf("%s/oauth/client-metadata.json", clientUri)
		callbackUri := clientUri + "/oauth/callback"
		oauthConfig = oauth.NewPublicConfig(clientId, callbackUri, []string{"atproto", "transition:generic"})
	}

	// configure client secret
	priv, err := atcrypto.ParsePrivateMultibase(config.OAuth.ClientSecret)
	if err != nil {
		return nil, err
	}
	if err := oauthConfig.SetClientSecret(priv, config.OAuth.ClientKid); err != nil {
		return nil, err
	}

	jwksUri := clientUri + "/oauth/jwks.json"

	authStore, err := NewRedisStore(&RedisStoreConfig{
		RedisURL:                  config.Redis.ToURL(),
		SessionExpiryDuration:     time.Hour * 24 * 90,
		SessionInactivityDuration: time.Hour * 24 * 14,
		AuthRequestExpiryDuration: time.Minute * 30,
	})
	if err != nil {
		return nil, err
	}

	sessStore := sessions.NewCookieStore([]byte(config.Core.CookieSecret))

	clientApp := oauth.NewClientApp(&oauthConfig, authStore)
	clientApp.Dir = res.Directory()
	// allow non-public transports in dev mode
	if config.Core.Dev {
		clientApp.Resolver.Client.Transport = http.DefaultTransport
	}

	clientName := config.Core.AppviewName

	logger.Info("oauth setup successfully", "IsConfidential", clientApp.Config.IsConfidential())
	return &OAuth{
		ClientApp:  clientApp,
		Config:     config,
		SessStore:  sessStore,
		JwksUri:    jwksUri,
		ClientName: clientName,
		ClientUri:  clientUri,
		Posthog:    ph,
		Db:         db,
		Enforcer:   enforcer,
		IdResolver: res,
		Logger:     logger,
	}, nil
}

func (o *OAuth) SaveSession(w http.ResponseWriter, r *http.Request, sessData *oauth.ClientSessionData) error {
	// first we save the did in the user session
	userSession, err := o.SessStore.Get(r, SessionName)
	if err != nil {
		return err
	}

	userSession.Values[SessionDid] = sessData.AccountDID.String()
	userSession.Values[SessionPds] = sessData.HostURL
	userSession.Values[SessionId] = sessData.SessionID
	userSession.Values[SessionAuthenticated] = true
	return userSession.Save(r, w)
}

func (o *OAuth) ResumeSession(r *http.Request) (*oauth.ClientSession, error) {
	userSession, err := o.SessStore.Get(r, SessionName)
	if err != nil {
		return nil, fmt.Errorf("error getting user session: %w", err)
	}
	if userSession.IsNew {
		return nil, fmt.Errorf("no session available for user")
	}

	d := userSession.Values[SessionDid].(string)
	sessDid, err := syntax.ParseDID(d)
	if err != nil {
		return nil, fmt.Errorf("malformed DID in session cookie '%s': %w", d, err)
	}

	sessId := userSession.Values[SessionId].(string)

	clientSess, err := o.ClientApp.ResumeSession(r.Context(), sessDid, sessId)
	if err != nil {
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	return clientSess, nil
}

func (o *OAuth) DeleteSession(w http.ResponseWriter, r *http.Request) error {
	userSession, err := o.SessStore.Get(r, SessionName)
	if err != nil {
		return fmt.Errorf("error getting user session: %w", err)
	}
	if userSession.IsNew {
		return fmt.Errorf("no session available for user")
	}

	d := userSession.Values[SessionDid].(string)
	sessDid, err := syntax.ParseDID(d)
	if err != nil {
		return fmt.Errorf("malformed DID in session cookie '%s': %w", d, err)
	}

	sessId := userSession.Values[SessionId].(string)

	// delete the session
	err1 := o.ClientApp.Logout(r.Context(), sessDid, sessId)

	// remove the cookie
	userSession.Options.MaxAge = -1
	err2 := o.SessStore.Save(r, w, userSession)

	return errors.Join(err1, err2)
}

type User struct {
	Did string
	Pds string
}

func (o *OAuth) GetUser(r *http.Request) *User {
	sess, err := o.ResumeSession(r)
	if err != nil {
		return nil
	}

	return &User{
		Did: sess.Data.AccountDID.String(),
		Pds: sess.Data.HostURL,
	}
}

func (o *OAuth) GetDid(r *http.Request) string {
	if u := o.GetUser(r); u != nil {
		return u.Did
	}

	return ""
}

func (o *OAuth) AuthorizedClient(r *http.Request) (*atpclient.APIClient, error) {
	session, err := o.ResumeSession(r)
	if err != nil {
		return nil, fmt.Errorf("error getting session: %w", err)
	}
	return session.APIClient(), nil
}

// this is a higher level abstraction on ServerGetServiceAuth
type ServiceClientOpts struct {
	service string
	exp     int64
	lxm     string
	dev     bool
}

type ServiceClientOpt func(*ServiceClientOpts)

func WithService(service string) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.service = service
	}
}

// Specify the Duration in seconds for the expiry of this token
//
// The time of expiry is calculated as time.Now().Unix() + exp
func WithExp(exp int64) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.exp = time.Now().Unix() + exp
	}
}

func WithLxm(lxm string) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.lxm = lxm
	}
}

func WithDev(dev bool) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.dev = dev
	}
}

func (s *ServiceClientOpts) Audience() string {
	return fmt.Sprintf("did:web:%s", s.service)
}

func (s *ServiceClientOpts) Host() string {
	scheme := "https://"
	if s.dev {
		scheme = "http://"
	}

	return scheme + s.service
}

func (o *OAuth) ServiceClient(r *http.Request, os ...ServiceClientOpt) (*xrpc.Client, error) {
	opts := ServiceClientOpts{}
	for _, o := range os {
		o(&opts)
	}

	client, err := o.AuthorizedClient(r)
	if err != nil {
		return nil, err
	}

	// force expiry to atleast 60 seconds in the future
	sixty := time.Now().Unix() + 60
	if opts.exp < sixty {
		opts.exp = sixty
	}

	resp, err := comatproto.ServerGetServiceAuth(r.Context(), client, opts.Audience(), opts.exp, opts.lxm)
	if err != nil {
		return nil, err
	}

	return &xrpc.Client{
		Auth: &xrpc.AuthInfo{
			AccessJwt: resp.Token,
		},
		Host: opts.Host(),
		Client: &http.Client{
			Timeout: time.Second * 5,
		},
	}, nil
}
