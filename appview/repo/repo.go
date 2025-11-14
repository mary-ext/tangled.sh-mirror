package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/validator"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/eventconsumer"
	"tangled.org/core/idresolver"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"
	"tangled.org/core/xrpc/serviceauth"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	atpclient "github.com/bluesky-social/indigo/atproto/client"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
)

type Repo struct {
	repoResolver  *reporesolver.RepoResolver
	idResolver    *idresolver.Resolver
	config        *config.Config
	oauth         *oauth.OAuth
	pages         *pages.Pages
	spindlestream *eventconsumer.Consumer
	db            *db.DB
	enforcer      *rbac.Enforcer
	notifier      notify.Notifier
	logger        *slog.Logger
	serviceAuth   *serviceauth.ServiceAuth
	validator     *validator.Validator
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	spindlestream *eventconsumer.Consumer,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	notifier notify.Notifier,
	enforcer *rbac.Enforcer,
	logger *slog.Logger,
	validator *validator.Validator,
) *Repo {
	return &Repo{oauth: oauth,
		repoResolver:  repoResolver,
		pages:         pages,
		idResolver:    idResolver,
		config:        config,
		spindlestream: spindlestream,
		db:            db,
		notifier:      notifier,
		enforcer:      enforcer,
		logger:        logger,
		validator:     validator,
	}
}

// modify the spindle configured for this repo
func (rp *Repo) EditSpindle(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "EditSpindle")
	l = l.With("did", user.Did)

	errorId := "operation-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		fail("Failed to resolve repo. Try again later", err)
		return
	}

	newSpindle := r.FormValue("spindle")
	removingSpindle := newSpindle == "[[none]]" // see pages/templates/repo/settings/pipelines.html for more info on why we use this value
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail("Failed to authorize. Try again later.", err)
		return
	}

	if !removingSpindle {
		// ensure that this is a valid spindle for this user
		validSpindles, err := rp.enforcer.GetSpindlesForUser(user.Did)
		if err != nil {
			fail("Failed to find spindles. Try again later.", err)
			return
		}

		if !slices.Contains(validSpindles, newSpindle) {
			fail("Failed to configure spindle.", fmt.Errorf("%s is not a valid spindle: %q", newSpindle, validSpindles))
			return
		}
	}

	newRepo := f.Repo
	newRepo.Spindle = newSpindle
	record := newRepo.AsRecord()

	spindlePtr := &newSpindle
	if removingSpindle {
		spindlePtr = nil
		newRepo.Spindle = ""
	}

	// optimistic update
	err = db.UpdateSpindle(rp.db, newRepo.RepoAt().String(), spindlePtr)
	if err != nil {
		fail("Failed to update spindle. Try again later.", err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update spindle, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})

	if err != nil {
		fail("Failed to update spindle, unable to save to PDS.", err)
		return
	}

	if !removingSpindle {
		// add this spindle to spindle stream
		rp.spindlestream.AddSource(
			context.Background(),
			eventconsumer.NewSpindleSource(newSpindle),
		)
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) AddLabelDef(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "AddLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "add-label-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	// get form values for label definition
	name := r.FormValue("name")
	concreteType := r.FormValue("valueType")
	valueFormat := r.FormValue("valueFormat")
	enumValues := r.FormValue("enumValues")
	scope := r.Form["scope"]
	color := r.FormValue("color")
	multiple := r.FormValue("multiple") == "true"

	var variants []string
	for part := range strings.SplitSeq(enumValues, ",") {
		if part = strings.TrimSpace(part); part != "" {
			variants = append(variants, part)
		}
	}

	if concreteType == "" {
		concreteType = "null"
	}

	format := models.ValueTypeFormatAny
	if valueFormat == "did" {
		format = models.ValueTypeFormatDid
	}

	valueType := models.ValueType{
		Type:   models.ConcreteType(concreteType),
		Format: format,
		Enum:   variants,
	}

	label := models.LabelDefinition{
		Did:       user.Did,
		Rkey:      tid.TID(),
		Name:      name,
		ValueType: valueType,
		Scope:     scope,
		Color:     &color,
		Multiple:  multiple,
		Created:   time.Now(),
	}
	if err := rp.validator.ValidateLabelDefinition(&label); err != nil {
		fail(err.Error(), err)
		return
	}

	// announce this relation into the firehose, store into owners' pds
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	// emit a labelRecord
	labelRecord := label.AsRecord()
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.LabelDefinitionNSID,
		Repo:       label.Did,
		Rkey:       label.Rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &labelRecord,
		},
	})
	// invalid record
	if err != nil {
		fail("Failed to write record to PDS.", err)
		return
	}

	aturi := resp.Uri
	l = l.With("at-uri", aturi)
	l.Info("wrote label record to PDS")

	// update the repo to subscribe to this label
	newRepo := f.Repo
	newRepo.Labels = append(newRepo.Labels, aturi)
	repoRecord := newRepo.AsRecord()

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})
	if err != nil {
		fail("Failed to update labels for repo.", err)
		return
	}

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	rollback := func() {
		err1 := tx.Rollback()
		err2 := rollbackRecord(context.Background(), aturi, client)

		// ignore txn complete errors, this is okay
		if errors.Is(err1, sql.ErrTxDone) {
			err1 = nil
		}

		if errs := errors.Join(err1, err2); errs != nil {
			l.Error("failed to rollback changes", "errs", errs)
			return
		}
	}
	defer rollback()

	_, err = db.AddLabelDefinition(tx, &label)
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	err = db.SubscribeLabel(tx, &models.RepoLabel{
		RepoAt:  f.RepoAt(),
		LabelAt: label.AtUri(),
	})

	err = tx.Commit()
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	// clear aturi when everything is successful
	aturi = ""

	rp.pages.HxRefresh(w)
}

func (rp *Repo) DeleteLabelDef(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "DeleteLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	// get form values
	labelId := r.FormValue("label-id")

	label, err := db.GetLabelDefinition(rp.db, db.FilterEq("id", labelId))
	if err != nil {
		fail("Failed to find label definition.", err)
		return
	}

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	// delete label record from PDS
	_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.LabelDefinitionNSID,
		Repo:       label.Did,
		Rkey:       label.Rkey,
	})
	if err != nil {
		fail("Failed to delete label record from PDS.", err)
		return
	}

	// update repo record to remove the label reference
	newRepo := f.Repo
	var updated []string
	removedAt := label.AtUri().String()
	for _, l := range newRepo.Labels {
		if l != removedAt {
			updated = append(updated, l)
		}
	}
	newRepo.Labels = updated
	repoRecord := newRepo.AsRecord()

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})
	if err != nil {
		fail("Failed to update repo record.", err)
		return
	}

	// transaction for DB changes
	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to delete label.", err)
		return
	}
	defer tx.Rollback()

	err = db.UnsubscribeLabel(
		tx,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("label_at", removedAt),
	)
	if err != nil {
		fail("Failed to unsubscribe label.", err)
		return
	}

	err = db.DeleteLabelDefinition(tx, db.FilterEq("id", label.Id))
	if err != nil {
		fail("Failed to delete label definition.", err)
		return
	}

	err = tx.Commit()
	if err != nil {
		fail("Failed to delete label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) SubscribeLabel(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "SubscribeLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		l.Error("invalid form", "err", err)
		return
	}

	errorId := "default-label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	labelAts := r.Form["label"]
	_, err = db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}

	newRepo := f.Repo
	newRepo.Labels = append(newRepo.Labels, labelAts...)

	// dedup
	slices.Sort(newRepo.Labels)
	newRepo.Labels = slices.Compact(newRepo.Labels)

	repoRecord := newRepo.AsRecord()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, f.Repo.Did, f.Repo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})

	tx, err := rp.db.Begin()
	if err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}
	defer tx.Rollback()

	for _, l := range labelAts {
		err = db.SubscribeLabel(tx, &models.RepoLabel{
			RepoAt:  f.RepoAt(),
			LabelAt: syntax.ATURI(l),
		})
		if err != nil {
			fail("Failed to subscribe to label.", err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) UnsubscribeLabel(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "UnsubscribeLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		l.Error("invalid form", "err", err)
		return
	}

	errorId := "default-label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	labelAts := r.Form["label"]
	_, err = db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		fail("Failed to unsubscribe to label.", err)
		return
	}

	// update repo record to remove the label reference
	newRepo := f.Repo
	var updated []string
	for _, l := range newRepo.Labels {
		if !slices.Contains(labelAts, l) {
			updated = append(updated, l)
		}
	}
	newRepo.Labels = updated
	repoRecord := newRepo.AsRecord()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, f.Repo.Did, f.Repo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})

	err = db.UnsubscribeLabel(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterIn("label_at", labelAts),
	)
	if err != nil {
		fail("Failed to unsubscribe label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) LabelPanel(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "LabelPanel")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	subjectStr := r.FormValue("subject")
	subject, err := syntax.ParseATURI(subjectStr)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", subject.Collection().String()),
	)
	if err != nil {
		l.Error("failed to fetch label defs", "err", err)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	states, err := db.GetLabels(rp.db, db.FilterEq("subject", subject))
	if err != nil {
		l.Error("failed to build label state", "err", err)
		return
	}
	state := states[subject]

	user := rp.oauth.GetUser(r)
	rp.pages.LabelPanel(w, pages.LabelPanelParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Defs:         defs,
		Subject:      subject.String(),
		State:        state,
	})
}

func (rp *Repo) EditLabelPanel(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "EditLabelPanel")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	subjectStr := r.FormValue("subject")
	subject, err := syntax.ParseATURI(subjectStr)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", subject.Collection().String()),
	)
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	states, err := db.GetLabels(rp.db, db.FilterEq("subject", subject))
	if err != nil {
		l.Error("failed to build label state", "err", err)
		return
	}
	state := states[subject]

	user := rp.oauth.GetUser(r)
	rp.pages.EditLabelPanel(w, pages.EditLabelPanelParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Defs:         defs,
		Subject:      subject.String(),
		State:        state,
	})
}

func (rp *Repo) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "AddCollaborator")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "add-collaborator-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	collaborator := r.FormValue("collaborator")
	if collaborator == "" {
		fail("Invalid form.", nil)
		return
	}

	// remove a single leading `@`, to make @handle work with ResolveIdent
	collaborator = strings.TrimPrefix(collaborator, "@")

	collaboratorIdent, err := rp.idResolver.ResolveIdent(r.Context(), collaborator)
	if err != nil {
		fail(fmt.Sprintf("'%s' is not a valid DID/handle.", collaborator), err)
		return
	}

	if collaboratorIdent.DID.String() == user.Did {
		fail("You seem to be adding yourself as a collaborator.", nil)
		return
	}
	l = l.With("collaborator", collaboratorIdent.Handle)
	l = l.With("knot", f.Knot)

	// announce this relation into the firehose, store into owners' pds
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail("Failed to write to PDS.", err)
		return
	}

	// emit a record
	currentUser := rp.oauth.GetUser(r)
	rkey := tid.TID()
	createdAt := time.Now()
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoCollaboratorNSID,
		Repo:       currentUser.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoCollaborator{
				Subject:   collaboratorIdent.DID.String(),
				Repo:      string(f.RepoAt()),
				CreatedAt: createdAt.Format(time.RFC3339),
			}},
	})
	// invalid record
	if err != nil {
		fail("Failed to write record to PDS.", err)
		return
	}

	aturi := resp.Uri
	l = l.With("at-uri", aturi)
	l.Info("wrote record to PDS")

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	rollback := func() {
		err1 := tx.Rollback()
		err2 := rp.enforcer.E.LoadPolicy()
		err3 := rollbackRecord(context.Background(), aturi, client)

		// ignore txn complete errors, this is okay
		if errors.Is(err1, sql.ErrTxDone) {
			err1 = nil
		}

		if errs := errors.Join(err1, err2, err3); errs != nil {
			l.Error("failed to rollback changes", "errs", errs)
			return
		}
	}
	defer rollback()

	err = rp.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.DidSlashRepo())
	if err != nil {
		fail("Failed to add collaborator permissions.", err)
		return
	}

	err = db.AddCollaborator(tx, models.Collaborator{
		Did:        syntax.DID(currentUser.Did),
		Rkey:       rkey,
		SubjectDid: collaboratorIdent.DID,
		RepoAt:     f.RepoAt(),
		Created:    createdAt,
	})
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	err = tx.Commit()
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	err = rp.enforcer.E.SavePolicy()
	if err != nil {
		fail("Failed to update collaborator permissions.", err)
		return
	}

	// clear aturi to when everything is successful
	aturi = ""

	rp.pages.HxRefresh(w)
}

func (rp *Repo) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "DeleteRepo")

	noticeId := "operation-error"
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	// remove record from pds
	atpClient, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to get authorized client", "err", err)
		return
	}
	_, err = comatproto.RepoDeleteRecord(r.Context(), atpClient, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       f.Rkey,
	})
	if err != nil {
		l.Error("failed to delete record", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to delete repository from PDS.")
		return
	}
	l.Info("removed repo record", "aturi", f.RepoAt().String())

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoDeleteNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to connect to knot server", "err", err)
		return
	}

	err = tangled.RepoDelete(
		r.Context(),
		client,
		&tangled.RepoDelete_Input{
			Did:  f.Did,
			Name: f.Name,
			Rkey: f.Rkey,
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}
	l.Info("deleted repo from knot")

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		l.Error("failed to start tx")
		w.Write(fmt.Append(nil, "failed to add collaborator: ", err))
		return
	}
	defer func() {
		tx.Rollback()
		err = rp.enforcer.E.LoadPolicy()
		if err != nil {
			l.Error("failed to rollback policies")
		}
	}()

	// remove collaborator RBAC
	repoCollaborators, err := rp.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to remove collaborators")
		return
	}
	for _, c := range repoCollaborators {
		did := c[0]
		rp.enforcer.RemoveCollaborator(did, f.Knot, f.DidSlashRepo())
	}
	l.Info("removed collaborators")

	// remove repo RBAC
	err = rp.enforcer.RemoveRepo(f.Did, f.Knot, f.DidSlashRepo())
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to update RBAC rules")
		return
	}

	// remove repo from db
	err = db.RemoveRepo(tx, f.Did, f.Name)
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to update appview")
		return
	}
	l.Info("removed repo from db")

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit changes", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = rp.enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACLs", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rp.pages.HxRedirect(w, fmt.Sprintf("/%s", f.Did))
}

func (rp *Repo) SyncRepoFork(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "SyncRepoFork")

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to resolve source repo", "err", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		client, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(f.Knot),
			oauth.WithLxm(tangled.RepoForkSyncNSID),
			oauth.WithDev(rp.config.Core.Dev),
		)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to connect to knot server.")
			return
		}

		if f.Source == "" {
			rp.pages.Notice(w, "repo", "This repository is not a fork.")
			return
		}

		err = tangled.RepoForkSync(
			r.Context(),
			client,
			&tangled.RepoForkSync_Input{
				Did:    user.Did,
				Name:   f.Name,
				Source: f.Source,
				Branch: ref,
			},
		)
		if err := xrpcclient.HandleXrpcErr(err); err != nil {
			rp.pages.Notice(w, "repo", err.Error())
			return
		}

		rp.pages.HxRefresh(w)
		return
	}
}

func (rp *Repo) ForkRepo(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "ForkRepo")

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to resolve source repo", "err", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		user := rp.oauth.GetUser(r)
		knots, err := rp.enforcer.GetKnotsForUser(user.Did)
		if err != nil {
			rp.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		rp.pages.ForkRepo(w, pages.ForkRepoParams{
			LoggedInUser: user,
			Knots:        knots,
			RepoInfo:     f.RepoInfo(user),
		})

	case http.MethodPost:
		l := rp.logger.With("handler", "ForkRepo")

		targetKnot := r.FormValue("knot")
		if targetKnot == "" {
			rp.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}
		l = l.With("targetKnot", targetKnot)

		ok, err := rp.enforcer.E.Enforce(user.Did, targetKnot, targetKnot, "repo:create")
		if err != nil || !ok {
			rp.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		// choose a name for a fork
		forkName := r.FormValue("repo_name")
		if forkName == "" {
			rp.pages.Notice(w, "repo", "Repository name cannot be empty.")
			return
		}

		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(
			rp.db,
			db.FilterEq("did", user.Did),
			db.FilterEq("name", forkName),
		)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				l.Error("error fetching existing repo from db", "err", err)
				rp.pages.Notice(w, "repo", "Failed to fork this repository. Try again later.")
				return
			}
		} else if existingRepo != nil {
			// repo with this name already exists
			rp.pages.Notice(w, "repo", "A repository with this name already exists.")
			return
		}
		l = l.With("forkName", forkName)

		uri := "https"
		if rp.config.Core.Dev {
			uri = "http"
		}

		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.Did, f.Repo.Name)
		l = l.With("cloneUrl", forkSourceUrl)

		sourceAt := f.RepoAt().String()

		// create an atproto record for this fork
		rkey := tid.TID()
		repo := &models.Repo{
			Did:         user.Did,
			Name:        forkName,
			Knot:        targetKnot,
			Rkey:        rkey,
			Source:      sourceAt,
			Description: f.Repo.Description,
			Created:     time.Now(),
			Labels:      rp.config.Label.DefaultLabelDefs,
		}
		record := repo.AsRecord()

		atpClient, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to create xrpcclient", "err", err)
			rp.pages.Notice(w, "repo", "Failed to fork repository.")
			return
		}

		atresp, err := comatproto.RepoPutRecord(r.Context(), atpClient, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			l.Error("failed to write to PDS", "err", err)
			rp.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}

		aturi := atresp.Uri
		l = l.With("aturi", aturi)
		l.Info("wrote to PDS")

		tx, err := rp.db.BeginTx(r.Context(), nil)
		if err != nil {
			l.Info("txn failed", "err", err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// The rollback function reverts a few things on failure:
		// - the pending txn
		// - the ACLs
		// - the atproto record created
		rollback := func() {
			err1 := tx.Rollback()
			err2 := rp.enforcer.E.LoadPolicy()
			err3 := rollbackRecord(context.Background(), aturi, atpClient)

			// ignore txn complete errors, this is okay
			if errors.Is(err1, sql.ErrTxDone) {
				err1 = nil
			}

			if errs := errors.Join(err1, err2, err3); errs != nil {
				l.Error("failed to rollback changes", "errs", errs)
				return
			}
		}
		defer rollback()

		client, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(targetKnot),
			oauth.WithLxm(tangled.RepoCreateNSID),
			oauth.WithDev(rp.config.Core.Dev),
		)
		if err != nil {
			l.Error("could not create service client", "err", err)
			rp.pages.Notice(w, "repo", "Failed to connect to knot server.")
			return
		}

		err = tangled.RepoCreate(
			r.Context(),
			client,
			&tangled.RepoCreate_Input{
				Rkey:   rkey,
				Source: &forkSourceUrl,
			},
		)
		if err := xrpcclient.HandleXrpcErr(err); err != nil {
			rp.pages.Notice(w, "repo", err.Error())
			return
		}

		err = db.AddRepo(tx, repo)
		if err != nil {
			l.Error("failed to AddRepo", "err", err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = rp.enforcer.AddRepo(user.Did, targetKnot, p)
		if err != nil {
			l.Error("failed to add ACLs", "err", err)
			rp.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			l.Error("failed to commit changes", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = rp.enforcer.E.SavePolicy()
		if err != nil {
			l.Error("failed to update ACLs", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// reset the ATURI because the transaction completed successfully
		aturi = ""

		rp.notifier.NewRepo(r.Context(), repo)
		rp.pages.HxLocation(w, fmt.Sprintf("/%s/%s", user.Did, forkName))
	}
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, client *atpclient.APIClient) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}
