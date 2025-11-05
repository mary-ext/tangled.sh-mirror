package refresolver

import (
	"context"
	"log/slog"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/idresolver"
)

type Resolver struct {
	config     *config.Config
	idResolver *idresolver.Resolver
	execer     db.Execer
	logger     *slog.Logger
}

func New(
	config *config.Config,
	idResolver *idresolver.Resolver,
	execer db.Execer,
	logger *slog.Logger,
) *Resolver {
	return &Resolver{
		config,
		idResolver,
		execer,
		logger,
	}
}

func (r *Resolver) Resolve(ctx context.Context, source string) ([]syntax.DID, []syntax.ATURI) {
	l := r.logger.With("method", "Resolve")
	rawMentions, rawRefs := markup.FindReferences(r.config.Core.AppviewHost, source)
	l.Debug("found possible references", "mentions", rawMentions, "refs", rawRefs)
	idents := r.idResolver.ResolveIdents(ctx, rawMentions)
	var mentions []syntax.DID
	for _, ident := range idents {
		if ident != nil && !ident.Handle.IsInvalidHandle() {
			mentions = append(mentions, ident.DID)
		}
	}
	l.Debug("found mentions", "mentions", mentions)

	var resolvedRefs []models.ReferenceLink
	for _, rawRef := range rawRefs {
		ident, err := r.idResolver.ResolveIdent(ctx, rawRef.Handle)
		if err != nil || ident == nil || ident.Handle.IsInvalidHandle() {
			continue
		}
		rawRef.Handle = string(ident.DID)
		resolvedRefs = append(resolvedRefs, rawRef)
	}
	aturiRefs, err := db.ValidateReferenceLinks(r.execer, resolvedRefs)
	if err != nil {
		l.Error("failed running query", "err", err)
	}
	l.Debug("found references", "refs", aturiRefs)

	return mentions, aturiRefs
}
