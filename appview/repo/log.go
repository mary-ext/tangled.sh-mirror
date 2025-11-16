package repo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/commitverify"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/types"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func (rp *Repo) Log(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoLog")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
		return
	}

	page := 1
	if r.URL.Query().Get("page") != "" {
		page, err = strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			page = 1
		}
	}

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	limit := int64(60)
	cursor := ""
	if page > 1 {
		// Convert page number to cursor (offset)
		offset := (page - 1) * int(limit)
		cursor = strconv.Itoa(offset)
	}

	repo := fmt.Sprintf("%s/%s", f.Did, f.Name)
	xrpcBytes, err := tangled.RepoLog(r.Context(), xrpcc, cursor, limit, "", ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.log", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var xrpcResp types.RepoLogResponse
	if err := json.Unmarshal(xrpcBytes, &xrpcResp); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	tagMap := make(map[string][]string)
	if tagBytes != nil {
		var tagResp types.RepoTagsResponse
		if err := json.Unmarshal(tagBytes, &tagResp); err == nil {
			for _, tag := range tagResp.Tags {
				hash := tag.Hash
				if tag.Tag != nil {
					hash = tag.Tag.Target.String()
				}
				tagMap[hash] = append(tagMap[hash], tag.Name)
			}
		}
	}

	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	if branchBytes != nil {
		var branchResp types.RepoBranchesResponse
		if err := json.Unmarshal(branchBytes, &branchResp); err == nil {
			for _, branch := range branchResp.Branches {
				tagMap[branch.Hash] = append(tagMap[branch.Hash], branch.Name)
			}
		}
	}

	user := rp.oauth.GetUser(r)

	emailToDidMap, err := db.GetEmailToDid(rp.db, uniqueEmails(xrpcResp.Commits), true)
	if err != nil {
		l.Error("failed to fetch email to did mapping", "err", err)
	}

	vc, err := commitverify.GetVerifiedObjectCommits(rp.db, emailToDidMap, xrpcResp.Commits)
	if err != nil {
		l.Error("failed to GetVerifiedObjectCommits", "err", err)
	}

	var shas []string
	for _, c := range xrpcResp.Commits {
		shas = append(shas, c.Hash.String())
	}
	pipelines, err := getPipelineStatuses(rp.db, &f.Repo, shas)
	if err != nil {
		l.Error("failed to getPipelineStatuses", "err", err)
		// non-fatal
	}

	rp.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser:    user,
		TagMap:          tagMap,
		RepoInfo:        rp.repoResolver.GetRepoInfo(r, user),
		RepoLogResponse: xrpcResp,
		EmailToDid:      emailToDidMap,
		VerifiedCommits: vc,
		Pipelines:       pipelines,
	})
}

func (rp *Repo) Commit(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCommit")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
		return
	}
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	if !plumbing.IsHash(ref) {
		rp.pages.Error404(w)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.Did, f.Name)
	xrpcBytes, err := tangled.RepoDiff(r.Context(), xrpcc, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.diff", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoCommitResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	emailToDidMap, err := db.GetEmailToDid(rp.db, []string{result.Diff.Commit.Committer.Email, result.Diff.Commit.Author.Email}, true)
	if err != nil {
		l.Error("failed to get email to did mapping", "err", err)
	}

	vc, err := commitverify.GetVerifiedCommits(rp.db, emailToDidMap, []types.NiceDiff{*result.Diff})
	if err != nil {
		l.Error("failed to GetVerifiedCommits", "err", err)
	}

	user := rp.oauth.GetUser(r)
	pipelines, err := getPipelineStatuses(rp.db, &f.Repo, []string{result.Diff.Commit.This})
	if err != nil {
		l.Error("failed to getPipelineStatuses", "err", err)
		// non-fatal
	}
	var pipeline *models.Pipeline
	if p, ok := pipelines[result.Diff.Commit.This]; ok {
		pipeline = &p
	}

	rp.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser:       user,
		RepoInfo:           rp.repoResolver.GetRepoInfo(r, user),
		RepoCommitResponse: result,
		EmailToDid:         emailToDidMap,
		VerifiedCommit:     vc,
		Pipeline:           pipeline,
		DiffOpts:           diffOpts,
	})
}
