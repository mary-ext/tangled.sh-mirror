package repo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
)

type tab = map[string]any

var (
	// would be great to have ordered maps right about now
	settingsTabs []tab = []tab{
		{"Name": "general", "Icon": "sliders-horizontal"},
		{"Name": "access", "Icon": "users"},
		{"Name": "pipelines", "Icon": "layers-2"},
	}
)

func (rp *Repo) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "SetDefaultBranch")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	noticeId := "operation-error"
	branch := r.FormValue("branch")
	if branch == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoSetDefaultBranchNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to connect to knot server", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to connect to knot server.")
		return
	}

	xe := tangled.RepoSetDefaultBranch(
		r.Context(),
		client,
		&tangled.RepoSetDefaultBranch_Input{
			Repo:          f.RepoAt().String(),
			DefaultBranch: branch,
		},
	)
	if err := xrpcclient.HandleXrpcErr(xe); err != nil {
		l.Error("xrpc failed", "err", xe)
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) Secrets(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "Secrets")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if f.Spindle == "" {
		l.Error("empty spindle cannot add/rm secret", "err", err)
		return
	}

	lxm := tangled.RepoAddSecretNSID
	if r.Method == http.MethodDelete {
		lxm = tangled.RepoRemoveSecretNSID
	}

	spindleClient, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Spindle),
		oauth.WithLxm(lxm),
		oauth.WithExp(60),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to create spindle client", "err", err)
		return
	}

	key := r.FormValue("key")
	if key == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		errorId := "add-secret-error"

		value := r.FormValue("value")
		if value == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		err = tangled.RepoAddSecret(
			r.Context(),
			spindleClient,
			&tangled.RepoAddSecret_Input{
				Repo:  f.RepoAt().String(),
				Key:   key,
				Value: value,
			},
		)
		if err != nil {
			l.Error("Failed to add secret.", "err", err)
			rp.pages.Notice(w, errorId, "Failed to add secret.")
			return
		}

	case http.MethodDelete:
		errorId := "operation-error"

		err = tangled.RepoRemoveSecret(
			r.Context(),
			spindleClient,
			&tangled.RepoRemoveSecret_Input{
				Repo: f.RepoAt().String(),
				Key:  key,
			},
		)
		if err != nil {
			l.Error("Failed to delete secret.", "err", err)
			rp.pages.Notice(w, errorId, "Failed to delete secret.")
			return
		}
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) Settings(w http.ResponseWriter, r *http.Request) {
	tabVal := r.URL.Query().Get("tab")
	if tabVal == "" {
		tabVal = "general"
	}

	switch tabVal {
	case "general":
		rp.generalSettings(w, r)

	case "access":
		rp.accessSettings(w, r)

	case "pipelines":
		rp.pipelineSettings(w, r)
	}
}

func (rp *Repo) generalSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "generalSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.Did, f.Name)
	xrpcBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	defaultLabels, err := db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", rp.config.Label.DefaultLabelDefs))
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}

	labels, err := db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", f.Labels))
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}
	// remove default labels from the labels list, if present
	defaultLabelMap := make(map[string]bool)
	for _, dl := range defaultLabels {
		defaultLabelMap[dl.AtUri().String()] = true
	}
	n := 0
	for _, l := range labels {
		if !defaultLabelMap[l.AtUri().String()] {
			labels[n] = l
			n++
		}
	}
	labels = labels[:n]

	subscribedLabels := make(map[string]struct{})
	for _, l := range f.Labels {
		subscribedLabels[l] = struct{}{}
	}

	// if there is atleast 1 unsubbed default label, show the "subscribe all" button,
	// if all default labels are subbed, show the "unsubscribe all" button
	shouldSubscribeAll := false
	for _, dl := range defaultLabels {
		if _, ok := subscribedLabels[dl.AtUri().String()]; !ok {
			// one of the default labels is not subscribed to
			shouldSubscribeAll = true
			break
		}
	}

	rp.pages.RepoGeneralSettings(w, pages.RepoGeneralSettingsParams{
		LoggedInUser:       user,
		RepoInfo:           rp.repoResolver.GetRepoInfo(r, user),
		Branches:           result.Branches,
		Labels:             labels,
		DefaultLabels:      defaultLabels,
		SubscribedLabels:   subscribedLabels,
		ShouldSubscribeAll: shouldSubscribeAll,
		Tabs:               settingsTabs,
		Tab:                "general",
	})
}

func (rp *Repo) accessSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "accessSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	collaborators, err := func(repo *models.Repo) ([]pages.Collaborator, error) {
		repoCollaborators, err := rp.enforcer.E.GetImplicitUsersForResourceByDomain(repo.DidSlashRepo(), repo.Knot)
		if err != nil {
			return nil, err
		}
		var collaborators []pages.Collaborator
		for _, item := range repoCollaborators {
			// currently only two roles: owner and member
			var role string
			switch item[3] {
			case "repo:owner":
				role = "owner"
			case "repo:collaborator":
				role = "collaborator"
			default:
				continue
			}

			did := item[0]

			c := pages.Collaborator{
				Did:  did,
				Role: role,
			}
			collaborators = append(collaborators, c)
		}
		return collaborators, nil
	}(f)
	if err != nil {
		l.Error("failed to get collaborators", "err", err)
	}

	rp.pages.RepoAccessSettings(w, pages.RepoAccessSettingsParams{
		LoggedInUser:  user,
		RepoInfo:      rp.repoResolver.GetRepoInfo(r, user),
		Tabs:          settingsTabs,
		Tab:           "access",
		Collaborators: collaborators,
	})
}

func (rp *Repo) pipelineSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "pipelineSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	// all spindles that the repo owner is a member of
	spindles, err := rp.enforcer.GetSpindlesForUser(f.Did)
	if err != nil {
		l.Error("failed to fetch spindles", "err", err)
		return
	}

	var secrets []*tangled.RepoListSecrets_Secret
	if f.Spindle != "" {
		if spindleClient, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(f.Spindle),
			oauth.WithLxm(tangled.RepoListSecretsNSID),
			oauth.WithExp(60),
			oauth.WithDev(rp.config.Core.Dev),
		); err != nil {
			l.Error("failed to create spindle client", "err", err)
		} else if resp, err := tangled.RepoListSecrets(r.Context(), spindleClient, f.RepoAt().String()); err != nil {
			l.Error("failed to fetch secrets", "err", err)
		} else {
			secrets = resp.Secrets
		}
	}

	slices.SortFunc(secrets, func(a, b *tangled.RepoListSecrets_Secret) int {
		return strings.Compare(a.Key, b.Key)
	})

	var dids []string
	for _, s := range secrets {
		dids = append(dids, s.CreatedBy)
	}
	resolvedIdents := rp.idResolver.ResolveIdents(r.Context(), dids)

	// convert to a more manageable form
	var niceSecret []map[string]any
	for id, s := range secrets {
		when, _ := time.Parse(time.RFC3339, s.CreatedAt)
		niceSecret = append(niceSecret, map[string]any{
			"Id":        id,
			"Key":       s.Key,
			"CreatedAt": when,
			"CreatedBy": resolvedIdents[id].Handle.String(),
		})
	}

	rp.pages.RepoPipelineSettings(w, pages.RepoPipelineSettingsParams{
		LoggedInUser:   user,
		RepoInfo:       rp.repoResolver.GetRepoInfo(r, user),
		Tabs:           settingsTabs,
		Tab:            "pipelines",
		Spindles:       spindles,
		CurrentSpindle: f.Spindle,
		Secrets:        niceSecret,
	})
}

func (rp *Repo) EditBaseSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "EditBaseSettings")

	noticeId := "repo-base-settings-error"

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to get client")
		rp.pages.Notice(w, noticeId, "Failed to update repository information, try again later.")
		return
	}

	var (
		description = r.FormValue("description")
		website     = r.FormValue("website")
		topicStr    = r.FormValue("topics")
	)

	err = rp.validator.ValidateURI(website)
	if website != "" && err != nil {
		l.Error("invalid uri", "err", err)
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}

	topics, err := rp.validator.ValidateRepoTopicStr(topicStr)
	if err != nil {
		l.Error("invalid topics", "err", err)
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}
	l.Debug("got", "topicsStr", topicStr, "topics", topics)

	newRepo := *f
	newRepo.Description = description
	newRepo.Website = website
	newRepo.Topics = topics
	record := newRepo.AsRecord()

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		l.Error("failed to begin transaction", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to save repository information.")
		return
	}
	defer tx.Rollback()

	err = db.PutRepo(tx, newRepo)
	if err != nil {
		l.Error("failed to update repository", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to save repository information.")
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		// failed to get record
		l.Error("failed to get repo record", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to save repository information, no record found on PDS.")
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
		l.Error("failed to perferom update-repo query", "err", err)
		// failed to get record
		rp.pages.Notice(w, noticeId, "Failed to save repository information, unable to save to PDS.")
		return
	}

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit", "err", err)
	}

	rp.pages.HxRefresh(w)
}
