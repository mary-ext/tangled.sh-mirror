package repo

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/types"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
)

func (rp *Repo) Branches(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBranches")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
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
	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
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
	sortBranches(result.Branches)
	user := rp.oauth.GetUser(r)
	rp.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(user),
		RepoBranchesResponse: result,
	})
}

func (rp *Repo) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DeleteBranch")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}
	noticeId := "delete-branch-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, noticeId, msg)
	}
	branch := r.FormValue("branch")
	if branch == "" {
		fail("No branch provided.", nil)
		return
	}
	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoDeleteBranchNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		fail("Failed to connect to knotserver", nil)
		return
	}
	err = tangled.RepoDeleteBranch(
		r.Context(),
		client,
		&tangled.RepoDeleteBranch_Input{
			Branch: branch,
			Repo:   f.RepoAt().String(),
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		fail(fmt.Sprintf("Failed to delete branch: %s", err), err)
		return
	}
	l.Error("deleted branch from knot", "branch", branch, "repo", f.RepoAt())
	rp.pages.HxRefresh(w)
}
