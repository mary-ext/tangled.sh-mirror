package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/xrpcclient"
	"tangled.org/core/tid"
	"tangled.org/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/dustin/go-humanize"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ipfs/go-cid"
)

// TODO: proper statuses here on early exit
func (rp *Repo) AttachArtifact(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	tagParam := chi.URLParam(r, "tag")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		rp.pages.Notice(w, "upload", "failed to upload artifact, error in repo resolution")
		return
	}

	tag, err := rp.resolveTag(r.Context(), f, tagParam)
	if err != nil {
		log.Println("failed to resolve tag", err)
		rp.pages.Notice(w, "upload", "failed to upload artifact, error in tag resolution")
		return
	}

	file, handler, err := r.FormFile("artifact")
	if err != nil {
		log.Println("failed to upload artifact", err)
		rp.pages.Notice(w, "upload", "failed to upload artifact")
		return
	}
	defer file.Close()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get authorized client", err)
		rp.pages.Notice(w, "upload", "failed to get authorized client")
		return
	}

	uploadBlobResp, err := comatproto.RepoUploadBlob(r.Context(), client, file)
	if err != nil {
		log.Println("failed to upload blob", err)
		rp.pages.Notice(w, "upload", "Failed to upload blob to your PDS. Try again later.")
		return
	}

	log.Println("uploaded blob", humanize.Bytes(uint64(uploadBlobResp.Blob.Size)), uploadBlobResp.Blob.Ref.String())

	rkey := tid.TID()
	createdAt := time.Now()

	putRecordResp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoArtifactNSID,
		Repo:       user.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoArtifact{
				Artifact:  uploadBlobResp.Blob,
				CreatedAt: createdAt.Format(time.RFC3339),
				Name:      handler.Filename,
				Repo:      f.RepoAt().String(),
				Tag:       tag.Tag.Hash[:],
			},
		},
	})
	if err != nil {
		log.Println("failed to create record", err)
		rp.pages.Notice(w, "upload", "Failed to create artifact record. Try again later.")
		return
	}

	log.Println(putRecordResp.Uri)

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		rp.pages.Notice(w, "upload", "Failed to create artifact. Try again later.")
		return
	}
	defer tx.Rollback()

	artifact := models.Artifact{
		Did:       user.Did,
		Rkey:      rkey,
		RepoAt:    f.RepoAt(),
		Tag:       tag.Tag.Hash,
		CreatedAt: createdAt,
		BlobCid:   cid.Cid(uploadBlobResp.Blob.Ref),
		Name:      handler.Filename,
		Size:      uint64(uploadBlobResp.Blob.Size),
		MimeType:  uploadBlobResp.Blob.MimeType,
	}

	err = db.AddArtifact(tx, artifact)
	if err != nil {
		log.Println("failed to add artifact record to db", err)
		rp.pages.Notice(w, "upload", "Failed to create artifact. Try again later.")
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to add artifact record to db")
		rp.pages.Notice(w, "upload", "Failed to create artifact. Try again later.")
		return
	}

	rp.pages.RepoArtifactFragment(w, pages.RepoArtifactParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Artifact:     artifact,
	})
}

func (rp *Repo) DownloadArtifact(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		http.Error(w, "failed to resolve repo", http.StatusInternalServerError)
		return
	}

	tagParam := chi.URLParam(r, "tag")
	filename := chi.URLParam(r, "file")

	tag, err := rp.resolveTag(r.Context(), f, tagParam)
	if err != nil {
		log.Println("failed to resolve tag", err)
		rp.pages.Notice(w, "upload", "failed to upload artifact, error in tag resolution")
		return
	}

	artifacts, err := db.GetArtifact(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("tag", tag.Tag.Hash[:]),
		db.FilterEq("name", filename),
	)
	if err != nil {
		log.Println("failed to get artifacts", err)
		http.Error(w, "failed to get artifact", http.StatusInternalServerError)
		return
	}

	if len(artifacts) != 1 {
		log.Printf("too many or too few artifacts found")
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	artifact := artifacts[0]

	ownerPds := f.OwnerId.PDSEndpoint()
	url, _ := url.Parse(fmt.Sprintf("%s/xrpc/com.atproto.sync.getBlob", ownerPds))
	q := url.Query()
	q.Set("cid", artifact.BlobCid.String())
	q.Set("did", artifact.Did)
	url.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, url.String(), nil)
	if err != nil {
		log.Println("failed to create request", err)
		http.Error(w, "failed to create request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("failed to make request", err)
		http.Error(w, "failed to make request to PDS", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// copy status code and relevant headers from upstream response
	w.WriteHeader(resp.StatusCode)
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	// stream the body directly to the client
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Println("error streaming response to client:", err)
	}
}

// TODO: proper statuses here on early exit
func (rp *Repo) DeleteArtifact(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	tagParam := chi.URLParam(r, "tag")
	filename := chi.URLParam(r, "file")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	client, _ := rp.oauth.AuthorizedClient(r)

	tag := plumbing.NewHash(tagParam)

	artifacts, err := db.GetArtifact(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("tag", tag[:]),
		db.FilterEq("name", filename),
	)
	if err != nil {
		log.Println("failed to get artifacts", err)
		rp.pages.Notice(w, "remove", "Failed to delete artifact. Try again later.")
		return
	}
	if len(artifacts) != 1 {
		rp.pages.Notice(w, "remove", "Unable to find artifact.")
		return
	}

	artifact := artifacts[0]

	if user.Did != artifact.Did {
		log.Println("user not authorized to delete artifact", err)
		rp.pages.Notice(w, "remove", "Unauthorized deletion of artifact.")
		return
	}

	_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoArtifactNSID,
		Repo:       user.Did,
		Rkey:       artifact.Rkey,
	})
	if err != nil {
		log.Println("failed to get blob from pds", err)
		rp.pages.Notice(w, "remove", "Failed to remove blob from PDS.")
		return
	}

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		rp.pages.Notice(w, "remove", "Failed to delete artifact. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.DeleteArtifact(tx,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("tag", artifact.Tag[:]),
		db.FilterEq("name", filename),
	)
	if err != nil {
		log.Println("failed to remove artifact record from db", err)
		rp.pages.Notice(w, "remove", "Failed to delete artifact. Try again later.")
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to remove artifact record from db")
		rp.pages.Notice(w, "remove", "Failed to delete artifact. Try again later.")
		return
	}

	w.Write([]byte{})
}

func (rp *Repo) resolveTag(ctx context.Context, f *reporesolver.ResolvedRepo, tagParam string) (*types.TagReference, error) {
	tagParam, err := url.QueryUnescape(tagParam)
	if err != nil {
		return nil, err
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
	xrpcBytes, err := tangled.RepoTags(ctx, xrpcc, "", 0, repo)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.tags", xrpcerr)
			return nil, xrpcerr
		}
		log.Println("failed to reach knotserver", err)
		return nil, err
	}

	var result types.RepoTagsResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC tags response", err)
		return nil, err
	}

	var tag *types.TagReference
	for _, t := range result.Tags {
		if t.Tag != nil {
			if t.Reference.Name == tagParam || t.Reference.Hash == tagParam {
				tag = t
			}
		}
	}

	if tag == nil {
		return nil, fmt.Errorf("invalid tag, only annotated tags are supported for artifacts")
	}

	if tag.Tag.Target.IsZero() {
		return nil, fmt.Errorf("invalid tag, only annotated tags are supported for artifacts")
	}

	return tag, nil
}
