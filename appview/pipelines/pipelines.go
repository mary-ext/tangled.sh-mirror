package pipelines

import (
	"bytes"
	"context"
	"encoding/json"
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
	defer func() {
		_ = clientConn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "log stream complete"),
			time.Now().Add(time.Second),
		)
		clientConn.Close()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

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
	evChan := make(chan logEvent, 100)
	// start a goroutine to read from spindle
	go readLogs(spindleConn, evChan)

	stepIdx := 0
	var fragment bytes.Buffer
	for {
		select {
		case <-ctx.Done():
			l.Info("client disconnected")
			return

		case ev, ok := <-evChan:
			if !ok {
				continue
			}

			if ev.err != nil && ev.isCloseError() {
				l.Debug("graceful shutdown, tail complete", "err", err)
				return
			}
			if ev.err != nil {
				l.Error("error reading from spindle", "err", err)
				return
			}

			var logLine spindlemodel.LogLine
			if err = json.Unmarshal(ev.msg, &logLine); err != nil {
				l.Error("failed to parse logline", "err", err)
				continue
			}

			fragment.Reset()

			switch logLine.Kind {
			case spindlemodel.LogKindControl:
				// control messages create a new step block
				stepIdx++
				collapsed := false
				if logLine.StepKind == spindlemodel.StepKindSystem {
					collapsed = true
				}
				err = p.pages.LogBlock(&fragment, pages.LogBlockParams{
					Id:        stepIdx,
					Name:      logLine.Content,
					Command:   logLine.StepCommand,
					Collapsed: collapsed,
				})
			case spindlemodel.LogKindData:
				// data messages simply insert new log lines into current step
				err = p.pages.LogLine(&fragment, pages.LogLineParams{
					Id:      stepIdx,
					Content: logLine.Content,
				})
			}
			if err != nil {
				l.Error("failed to render log line", "err", err)
				return
			}

			if err = clientConn.WriteMessage(websocket.TextMessage, fragment.Bytes()); err != nil {
				l.Error("error writing to client", "err", err)
				return
			}

		case <-time.After(30 * time.Second):
			l.Debug("sent keepalive")
			if err = clientConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second)); err != nil {
				l.Error("failed to write control", "err", err)
				return
			}
		}
	}
}

// either a message or an error
type logEvent struct {
	msg []byte
	err error
}

func (ev *logEvent) isCloseError() bool {
	return websocket.IsCloseError(
		ev.err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseAbnormalClosure,
	)
}

// read logs from spindle and pass through to chan
func readLogs(conn *websocket.Conn, ch chan logEvent) {
	defer close(ch)

	for {
		if conn == nil {
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			ch <- logEvent{err: err}
			return
		}
		ch <- logEvent{msg: msg}
	}
}
