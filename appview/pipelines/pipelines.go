package pipelines

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"
	spindlemodel "tangled.sh/tangled.sh/core/spindle/models"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/posthog/posthog-go"
)

type Pipelines struct {
	repoResolver  *reporesolver.RepoResolver
	idResolver    *idresolver.Resolver
	config        *config.Config
	oauth         *oauth.OAuth
	pages         *pages.Pages
	spindlestream *eventconsumer.Consumer
	db            *db.DB
	enforcer      *rbac.Enforcer
	posthog       posthog.Client
	logger        *slog.Logger
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	spindlestream *eventconsumer.Consumer,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	posthog posthog.Client,
	enforcer *rbac.Enforcer,
) *Pipelines {
	logger := log.New("pipelines")

	return &Pipelines{oauth: oauth,
		repoResolver:  repoResolver,
		pages:         pages,
		idResolver:    idResolver,
		config:        config,
		spindlestream: spindlestream,
		db:            db,
		posthog:       posthog,
		enforcer:      enforcer,
		logger:        logger,
	}
}

func (p *Pipelines) Index(w http.ResponseWriter, r *http.Request) {
	user := p.oauth.GetUser(r)
	l := p.logger.With("handler", "Index")

	f, err := p.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	repoInfo := f.RepoInfo(user)

	ps, err := db.GetPipelineStatuses(
		p.db,
		db.FilterEq("repo_owner", repoInfo.OwnerDid),
		db.FilterEq("repo_name", repoInfo.Name),
		db.FilterEq("knot", repoInfo.Knot),
	)
	if err != nil {
		l.Error("failed to query db", "err", err)
		return
	}

	p.pages.Pipelines(w, pages.PipelinesParams{
		LoggedInUser: user,
		RepoInfo:     repoInfo,
		Pipelines:    ps,
	})
}

func (p *Pipelines) Workflow(w http.ResponseWriter, r *http.Request) {
	user := p.oauth.GetUser(r)
	l := p.logger.With("handler", "Workflow")

	f, err := p.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	repoInfo := f.RepoInfo(user)

	pipelineId := chi.URLParam(r, "pipeline")
	if pipelineId == "" {
		l.Error("empty pipeline ID")
		return
	}

	workflow := chi.URLParam(r, "workflow")
	if workflow == "" {
		l.Error("empty workflow name")
		return
	}

	ps, err := db.GetPipelineStatuses(
		p.db,
		db.FilterEq("repo_owner", repoInfo.OwnerDid),
		db.FilterEq("repo_name", repoInfo.Name),
		db.FilterEq("knot", repoInfo.Knot),
		db.FilterEq("id", pipelineId),
	)
	if err != nil {
		l.Error("failed to query db", "err", err)
		return
	}

	if len(ps) != 1 {
		l.Error("invalid number of pipelines", "len", len(ps))
		return
	}

	singlePipeline := ps[0]

	p.pages.Workflow(w, pages.WorkflowParams{
		LoggedInUser: user,
		RepoInfo:     repoInfo,
		Pipeline:     singlePipeline,
		Workflow:     workflow,
	})
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (p *Pipelines) Logs(w http.ResponseWriter, r *http.Request) {
	l := p.logger.With("handler", "logs")

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		return
	}
	defer clientConn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := clientConn.NextReader(); err != nil {
				l.Error("failed to read", "err", err)
				cancel()
				return
			}
		}
	}()

	user := p.oauth.GetUser(r)
	f, err := p.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		http.Error(w, "bad repo/knot", http.StatusBadRequest)
		return
	}

	repoInfo := f.RepoInfo(user)

	pipelineId := chi.URLParam(r, "pipeline")
	workflow := chi.URLParam(r, "workflow")
	if pipelineId == "" || workflow == "" {
		http.Error(w, "missing pipeline ID or workflow", http.StatusBadRequest)
		return
	}

	ps, err := db.GetPipelineStatuses(
		p.db,
		db.FilterEq("repo_owner", repoInfo.OwnerDid),
		db.FilterEq("repo_name", repoInfo.Name),
		db.FilterEq("knot", repoInfo.Knot),
		db.FilterEq("id", pipelineId),
	)
	if err != nil || len(ps) != 1 {
		l.Error("pipeline query failed", "err", err, "count", len(ps))
		http.Error(w, "pipeline not found", http.StatusNotFound)
		return
	}

	singlePipeline := ps[0]
	spindle := repoInfo.Spindle
	knot := repoInfo.Knot
	rkey := singlePipeline.Rkey

	if spindle == "" || knot == "" || rkey == "" {
		http.Error(w, "invalid repo info", http.StatusBadRequest)
		return
	}

	scheme := "wss"
	if p.config.Core.Dev {
		scheme = "ws"
	}

	url := scheme + "://" + strings.Join([]string{spindle, "logs", knot, rkey, workflow}, "/")
	l = l.With("url", url)
	l.Info("logs endpoint hit")

	spindleConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		l.Error("websocket dial failed", "err", err)
		http.Error(w, "failed to connect to log stream", http.StatusBadGateway)
		return
	}
	defer spindleConn.Close()

	// create a channel for incoming messages
	msgChan := make(chan []byte, 10)
	errChan := make(chan error, 1)

	// start a goroutine to read from spindle
	go func() {
		defer close(msgChan)
		for {
			_, msg, err := spindleConn.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			msgChan <- msg
		}
	}()

	for {
		select {
		case <-ctx.Done():
			l.Info("client disconnected")
			return
		case err := <-errChan:
			l.Error("error reading from spindle", "err", err)
			return
		case msg := <-msgChan:
			var logLine spindlemodel.LogLine
			if err = json.Unmarshal(msg, &logLine); err != nil {
				l.Error("failed to parse logline", "err", err)
				continue
			}

			html := fmt.Appendf(nil, `
				<div id="lines" hx-swap-oob="beforeend">
				<p>%s: %s</p>
				</div>
			`, logLine.Stream, logLine.Data)

			if err = clientConn.WriteMessage(websocket.TextMessage, html); err != nil {
				l.Error("error writing to client", "err", err)
				return
			}
		case <-time.After(30 * time.Second):
			l.Debug("sent keepalive")
			if err = clientConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second)); err != nil {
				l.Error("failed to write control", "err", err)
			}
		}
	}
}
