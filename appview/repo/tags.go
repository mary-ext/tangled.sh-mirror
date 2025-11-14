package repo

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/types"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-git/go-git/v5/plumbing"
)

func (rp *Repo) Tags(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoTags")
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
	repo := fmt.Sprintf("%s/%s", f.Did, f.Name)
	xrpcBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}
	var result types.RepoTagsResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}
	artifacts, err := db.GetArtifact(rp.db, db.FilterEq("repo_at", f.RepoAt()))
	if err != nil {
		l.Error("failed grab artifacts", "err", err)
		return
	}
	// convert artifacts to map for easy UI building
	artifactMap := make(map[plumbing.Hash][]models.Artifact)
	for _, a := range artifacts {
		artifactMap[a.Tag] = append(artifactMap[a.Tag], a)
	}
	var danglingArtifacts []models.Artifact
	for _, a := range artifacts {
		found := false
		for _, t := range result.Tags {
			if t.Tag != nil {
				if t.Tag.Hash == a.Tag {
					found = true
				}
			}
		}
		if !found {
			danglingArtifacts = append(danglingArtifacts, a)
		}
	}
	user := rp.oauth.GetUser(r)
	rp.pages.RepoTags(w, pages.RepoTagsParams{
		LoggedInUser:      user,
		RepoInfo:          f.RepoInfo(user),
		RepoTagsResponse:  result,
		ArtifactMap:       artifactMap,
		DanglingArtifacts: danglingArtifacts,
	})
}
