package pipelines

import (
	"log/slog"
	"net/http"

	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"

	"github.com/go-chi/chi/v5"
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
	Logger        *slog.Logger
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
		Logger:        logger,
	}
}

func (p *Pipelines) Index(w http.ResponseWriter, r *http.Request) {
	user := p.oauth.GetUser(r)
	l := p.Logger.With("handler", "Index")

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
	l := p.Logger.With("handler", "Workflow")

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
	if pipelineId == "" {
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
