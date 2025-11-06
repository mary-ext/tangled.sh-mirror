package repo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/pages"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
)

func (rp *Repo) CompareNew(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCompareNew")

	user := rp.oauth.GetUser(r)
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
	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branchResult types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branchResult); err != nil {
		l.Error("failed to decode XRPC branches response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}
	branches := branchResult.Branches

	sortBranches(branches)

	var defaultBranch string
	for _, b := range branches {
		if b.IsDefault {
			defaultBranch = b.Name
		}
	}

	base := defaultBranch
	head := defaultBranch

	params := r.URL.Query()
	queryBase := params.Get("base")
	queryHead := params.Get("head")
	if queryBase != "" {
		base = queryBase
	}
	if queryHead != "" {
		head = queryHead
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		l.Error("failed to decode XRPC tags response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	repoinfo := f.RepoInfo(user)

	rp.pages.RepoCompareNew(w, pages.RepoCompareNewParams{
		LoggedInUser: user,
		RepoInfo:     repoinfo,
		Branches:     branches,
		Tags:         tags.Tags,
		Base:         base,
		Head:         head,
	})
}

func (rp *Repo) Compare(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCompare")

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	// if user is navigating to one of
	//   /compare/{base}/{head}
	//   /compare/{base}...{head}
	base := chi.URLParam(r, "base")
	head := chi.URLParam(r, "head")
	if base == "" && head == "" {
		rest := chi.URLParam(r, "*") // master...feature/xyz
		parts := strings.SplitN(rest, "...", 2)
		if len(parts) == 2 {
			base = parts[0]
			head = parts[1]
		}
	}

	base, _ = url.PathUnescape(base)
	head, _ = url.PathUnescape(head)

	if base == "" || head == "" {
		l.Error("invalid comparison")
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

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)

	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branches types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branches); err != nil {
		l.Error("failed to decode XRPC branches response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		l.Error("failed to decode XRPC tags response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	compareBytes, err := tangled.RepoCompare(r.Context(), xrpcc, repo, base, head)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.compare", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var formatPatch types.RepoFormatPatchResponse
	if err := json.Unmarshal(compareBytes, &formatPatch); err != nil {
		l.Error("failed to decode XRPC compare response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	var diff types.NiceDiff
	if formatPatch.CombinedPatchRaw != "" {
		diff = patchutil.AsNiceDiff(formatPatch.CombinedPatchRaw, base)
	} else {
		diff = patchutil.AsNiceDiff(formatPatch.FormatPatchRaw, base)
	}

	repoinfo := f.RepoInfo(user)

	rp.pages.RepoCompare(w, pages.RepoCompareParams{
		LoggedInUser: user,
		RepoInfo:     repoinfo,
		Branches:     branches.Branches,
		Tags:         tags.Tags,
		Base:         base,
		Head:         head,
		Diff:         &diff,
		DiffOpts:     diffOpts,
	})

}
