package state

import (
	"log/slog"

	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/indexer"
	"tangled.org/core/appview/issues"
	"tangled.org/core/appview/middleware"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/validator"
	"tangled.org/core/idresolver"
	"tangled.org/core/log"
	"tangled.org/core/rbac"
)

// Expose exposes private fields in `State`. This is used to bridge between
// legacy web routers and new architecture
func (s *State) Expose() (
	*config.Config,
	*db.DB,
	*rbac.Enforcer,
	*idresolver.Resolver,
	*indexer.Indexer,
	*slog.Logger,
	notify.Notifier,
	*oauth.OAuth,
	*pages.Pages,
	*validator.Validator,
) {
	return s.config, s.db, s.enforcer, s.idResolver, s.indexer, s.logger, s.notifier, s.oauth, s.pages, s.validator
}

func (s *State) ExposeIssue() *issues.Issues {
	return issues.New(
		s.oauth,
		s.repoResolver,
		s.pages,
		s.idResolver,
		s.db,
		s.config,
		s.notifier,
		s.validator,
		s.indexer.Issues,
		log.SubLogger(s.logger, "issues"),
	)
}

func (s *State) Middleware() *middleware.Middleware {
	mw := middleware.New(
		s.oauth,
		s.db,
		s.enforcer,
		s.repoResolver,
		s.idResolver,
		s.pages,
	)
	return &mw
}
