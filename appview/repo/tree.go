package repo

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/reporesolver"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/types"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func (rp *Repo) Tree(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoTree")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
		return
	}
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)
	// if the tree path has a trailing slash, let's strip it
	// so we don't 404
	treePath := chi.URLParam(r, "*")
	treePath, _ = url.PathUnescape(treePath)
	treePath = strings.TrimSuffix(treePath, "/")
	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}
	repo := fmt.Sprintf("%s/%s", f.Did, f.Name)
	xrpcResp, err := tangled.RepoTree(r.Context(), xrpcc, treePath, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tree", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}
	// Convert XRPC response to internal types.RepoTreeResponse
	files := make([]types.NiceTree, len(xrpcResp.Files))
	for i, xrpcFile := range xrpcResp.Files {
		file := types.NiceTree{
			Name: xrpcFile.Name,
			Mode: xrpcFile.Mode,
			Size: int64(xrpcFile.Size),
		}
		// Convert last commit info if present
		if xrpcFile.Last_commit != nil {
			commitWhen, _ := time.Parse(time.RFC3339, xrpcFile.Last_commit.When)
			file.LastCommit = &types.LastCommitInfo{
				Hash:    plumbing.NewHash(xrpcFile.Last_commit.Hash),
				Message: xrpcFile.Last_commit.Message,
				When:    commitWhen,
			}
		}
		files[i] = file
	}
	result := types.RepoTreeResponse{
		Ref:   xrpcResp.Ref,
		Files: files,
	}
	if xrpcResp.Parent != nil {
		result.Parent = *xrpcResp.Parent
	}
	if xrpcResp.Dotdot != nil {
		result.DotDot = *xrpcResp.Dotdot
	}
	if xrpcResp.Readme != nil {
		result.ReadmeFileName = xrpcResp.Readme.Filename
		result.Readme = xrpcResp.Readme.Contents
	}
	ownerSlashRepo := reporesolver.GetBaseRepoPath(r, &f.Repo)
	// redirects tree paths trying to access a blob; in this case the result.Files is unpopulated,
	// so we can safely redirect to the "parent" (which is the same file).
	if len(result.Files) == 0 && result.Parent == treePath {
		redirectTo := fmt.Sprintf("/%s/blob/%s/%s", ownerSlashRepo, url.PathEscape(ref), result.Parent)
		http.Redirect(w, r, redirectTo, http.StatusFound)
		return
	}
	user := rp.oauth.GetUser(r)
	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", ownerSlashRepo, url.PathEscape(ref))})
	if treePath != "" {
		for idx, elem := range strings.Split(treePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], url.PathEscape(elem))})
		}
	}
	sortFiles(result.Files)

	rp.pages.RepoTree(w, pages.RepoTreeParams{
		LoggedInUser:     user,
		BreadCrumbs:      breadcrumbs,
		TreePath:         treePath,
		RepoInfo:         rp.repoResolver.GetRepoInfo(r, user),
		RepoTreeResponse: result,
	})
}
