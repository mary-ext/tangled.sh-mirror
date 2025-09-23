package xrpc

import (
	"fmt"
	"net/http"

	"tangled.org/core/knotserver/git"
	"tangled.org/core/types"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) RepoCompare(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	rev1 := r.URL.Query().Get("rev1")
	if rev1 == "" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing rev1 parameter"),
		), http.StatusBadRequest)
		return
	}

	rev2 := r.URL.Query().Get("rev2")
	if rev2 == "" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing rev2 parameter"),
		), http.StatusBadRequest)
		return
	}

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNoContent)
		return
	}

	commit1, err := gr.ResolveRevision(rev1)
	if err != nil {
		x.Logger.Error("error resolving revision 1", "msg", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RevisionNotFound"),
			xrpcerr.WithMessage(fmt.Sprintf("error resolving revision %s", rev1)),
		), http.StatusBadRequest)
		return
	}

	commit2, err := gr.ResolveRevision(rev2)
	if err != nil {
		x.Logger.Error("error resolving revision 2", "msg", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RevisionNotFound"),
			xrpcerr.WithMessage(fmt.Sprintf("error resolving revision %s", rev2)),
		), http.StatusBadRequest)
		return
	}

	rawPatch, formatPatch, err := gr.FormatPatch(commit1, commit2)
	if err != nil {
		x.Logger.Error("error comparing revisions", "msg", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("CompareError"),
			xrpcerr.WithMessage("error comparing revisions"),
		), http.StatusBadRequest)
		return
	}

	response := types.RepoFormatPatchResponse{
		Rev1:        commit1.Hash.String(),
		Rev2:        commit2.Hash.String(),
		FormatPatch: formatPatch,
		Patch:       rawPatch,
	}

	writeJson(w, response)
}
