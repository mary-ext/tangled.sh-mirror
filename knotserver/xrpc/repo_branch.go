package xrpc

import (
	"net/http"
	"net/url"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/knotserver/git"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) RepoBranch(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing name parameter"),
		), http.StatusBadRequest)
		return
	}

	branchName, _ := url.PathUnescape(name)

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNoContent)
		return
	}

	ref, err := gr.Branch(branchName)
	if err != nil {
		x.Logger.Error("getting branch", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("BranchNotFound"),
			xrpcerr.WithMessage("branch not found"),
		), http.StatusNotFound)
		return
	}

	commit, err := gr.Commit(ref.Hash())
	if err != nil {
		x.Logger.Error("getting commit object", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("BranchNotFound"),
			xrpcerr.WithMessage("failed to get commit object"),
		), http.StatusInternalServerError)
		return
	}

	defaultBranch, err := gr.FindMainBranch()
	isDefault := false
	if err != nil {
		x.Logger.Error("getting default branch", "error", err.Error())
	} else if defaultBranch == branchName {
		isDefault = true
	}

	response := tangled.RepoBranch_Output{
		Name:      ref.Name().Short(),
		Hash:      ref.Hash().String(),
		ShortHash: &[]string{ref.Hash().String()[:7]}[0],
		When:      commit.Author.When.Format(time.RFC3339),
		IsDefault: &isDefault,
	}

	if commit.Message != "" {
		response.Message = &commit.Message
	}

	response.Author = &tangled.RepoBranch_Signature{
		Name:  commit.Author.Name,
		Email: commit.Author.Email,
		When:  commit.Author.When.Format(time.RFC3339),
	}

	writeJson(w, response)
}
