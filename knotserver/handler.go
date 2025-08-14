package knotserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/xrpc"
	tlog "tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/types"
)

type Handle struct {
	c        *config.Config
	db       *db.DB
	jc       *jetstream.JetstreamClient
	e        *rbac.Enforcer
	l        *slog.Logger
	n        *notifier.Notifier
	resolver *idresolver.Resolver
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, jc *jetstream.JetstreamClient, l *slog.Logger, n *notifier.Notifier) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:        c,
		db:       db,
		e:        e,
		l:        l,
		jc:       jc,
		n:        n,
		resolver: idresolver.DefaultResolver(),
	}

	err := e.AddKnot(rbac.ThisServer)
	if err != nil {
		return nil, fmt.Errorf("failed to setup enforcer: %w", err)
	}

	// configure owner
	if err = h.configureOwner(); err != nil {
		return nil, err
	}
	h.l.Info("owner set", "did", h.c.Server.Owner)
	h.jc.AddDid(h.c.Server.Owner)

	// configure known-dids in jetstream consumer
	dids, err := h.db.GetAllDids()
	if err != nil {
		return nil, fmt.Errorf("failed to get all dids: %w", err)
	}
	for _, d := range dids {
		jc.AddDid(d)
	}

	err = h.jc.StartJetstream(ctx, h.processMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
	}

	r.Get("/", h.Index)
	r.Get("/capabilities", h.Capabilities)
	r.Get("/version", h.Version)
	r.Get("/owner", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(h.c.Server.Owner))
	})
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Route("/collaborator", func(r chi.Router) {
				r.Use(h.VerifySignature)
				r.Post("/add", h.AddRepoCollaborator)
			})

			r.Route("/languages", func(r chi.Router) {
				r.With(h.VerifySignature)
				r.Get("/", h.RepoLanguages)
				r.Get("/{ref}", h.RepoLanguages)
			})

			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)
			r.Post("/git-receive-pack", h.ReceivePack)
			r.Get("/compare/{rev1}/{rev2}", h.Compare) // git diff-tree compare of two objects

			r.With(h.VerifySignature).Post("/hidden-ref/{forkRef}/{remoteRef}", h.NewHiddenRef)

			r.Route("/merge", func(r chi.Router) {
				r.With(h.VerifySignature)
				r.Post("/", h.Merge)
				r.Post("/check", h.MergeCheck)
			})

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/", h.RepoIndex)
				r.Get("/*", h.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", h.Blob)
			})

			r.Route("/raw/{ref}", func(r chi.Router) {
				r.Get("/*", h.BlobRaw)
			})

			r.Get("/log/{ref}", h.Log)
			r.Get("/archive/{file}", h.Archive)
			r.Get("/commit/{ref}", h.Diff)
			r.Get("/tags", h.Tags)
			r.Route("/branches", func(r chi.Router) {
				r.Get("/", h.Branches)
				r.Get("/{branch}", h.Branch)
				r.Route("/default", func(r chi.Router) {
					r.Get("/", h.DefaultBranch)
					r.With(h.VerifySignature).Put("/", h.SetDefaultBranch)
				})
			})
		})
	})

	// xrpc apis
	r.Mount("/xrpc", h.XrpcRouter())

	// Create a new repository.
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Delete("/", h.RemoveRepo)
		r.Route("/fork", func(r chi.Router) {
			r.Post("/", h.RepoFork)
			r.Post("/sync/*", h.RepoForkSync)
			r.Get("/sync/*", h.RepoForkAheadBehind)
		})
	})

	r.Route("/member", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/add", h.AddMember)
	})

	// Socket that streams git oplogs
	r.Get("/events", h.Events)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// All public keys on the knot.
	r.Get("/keys", h.Keys)

	return r, nil
}

func (h *Handle) XrpcRouter() http.Handler {
	logger := tlog.New("knots")

	serviceAuth := serviceauth.NewServiceAuth(h.l, h.resolver, h.c.Server.Did().String())

	xrpc := &xrpc.Xrpc{
		Config:      h.c,
		Db:          h.db,
		Ingester:    h.jc,
		Enforcer:    h.e,
		Logger:      logger,
		Notifier:    h.n,
		Resolver:    h.resolver,
		ServiceAuth: serviceAuth,
	}
	return xrpc.Router()
}

// version is set during build time.
var version string

func (h *Handle) Version(w http.ResponseWriter, r *http.Request) {
	if version == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			http.Error(w, "failed to read build info", http.StatusInternalServerError)
			return
		}

		var modVer string
		for _, mod := range info.Deps {
			if mod.Path == "tangled.sh/tangled.sh/knotserver" {
				version = mod.Version
				break
			}
		}

		if modVer == "" {
			version = "unknown"
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "knotserver/%s", version)
}

func (h *Handle) configureOwner() error {
	cfgOwner := h.c.Server.Owner

	rbacDomain := "thisserver"

	existing, err := h.e.GetKnotUsersByRole("server:owner", rbacDomain)
	if err != nil {
		return err
	}

	switch len(existing) {
	case 0:
		// no owner configured, continue
	case 1:
		// find existing owner
		existingOwner := existing[0]

		// no ownership change, this is okay
		if existingOwner == h.c.Server.Owner {
			break
		}

		// remove existing owner
		err = h.e.RemoveKnotOwner(rbacDomain, existingOwner)
		if err != nil {
			return nil
		}
	default:
		l.Error("attempted to serve disallowed file type", "mimetype", mimeType)
		writeError(w, "only image, video, and text files can be accessed directly", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Write(contents)
}

func (h *Handle) Blob(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "Blob", "ref", ref, "treePath", treePath)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	var isBinaryFile bool = false
	contents, err := gr.FileContent(treePath)
	if errors.Is(err, git.ErrBinaryFile) {
		isBinaryFile = true
	} else if errors.Is(err, object.ErrFileNotFound) {
		notFound(w)
		return
	} else if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bytes := []byte(contents)
	// safe := string(sanitize(bytes))
	sizeHint := len(bytes)

	resp := types.RepoBlobResponse{
		Ref:      ref,
		Contents: string(bytes),
		Path:     treePath,
		IsBinary: isBinaryFile,
		SizeHint: uint64(sizeHint),
	}

	h.showFile(resp, w, l)
}

func (h *Handle) Archive(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	file := chi.URLParam(r, "file")

	l := h.l.With("handler", "Archive", "name", name, "file", file)

	// TODO: extend this to add more files compression (e.g.: xz)
	if !strings.HasSuffix(file, ".tar.gz") {
		notFound(w)
		return
	}

	ref := strings.TrimSuffix(file, ".tar.gz")

	unescapedRef, err := url.PathUnescape(ref)
	if err != nil {
		notFound(w)
		return
	}

	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(unescapedRef).Short(), "/", "-")

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, safeRefFilename)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, unescapedRef)
	if err != nil {
		notFound(w)
		return
	}

	gw := gzip.NewWriter(w)
	defer gw.Close()

	prefix := fmt.Sprintf("%s-%s", name, safeRefFilename)
	err = gr.WriteTar(gw, prefix)
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		l.Error("writing tar file", "error", err.Error())
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		l.Error("flushing?", "error", err.Error())
		return
	}
}

func (h *Handle) Log(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	l := h.l.With("handler", "Log", "ref", ref, "path", path)

	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	// Get page parameters
	page := 1
	pageSize := 30

	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if p, err := strconv.Atoi(pageParam); err == nil && p > 0 {
			page = p
		}
	}

	if pageSizeParam := r.URL.Query().Get("per_page"); pageSizeParam != "" {
		if ps, err := strconv.Atoi(pageSizeParam); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	// convert to offset/limit
	offset := (page - 1) * pageSize
	limit := pageSize

	commits, err := gr.Commits(offset, limit)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("fetching commits", "error", err.Error())
		return
	}

	total := len(commits)

	resp := types.RepoLogResponse{
		Commits:     commits,
		Ref:         ref,
		Description: getDescription(path),
		Log:         true,
		Total:       total,
		Page:        page,
		PerPage:     pageSize,
	}

	writeJSON(w, resp)
}

func (h *Handle) Diff(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "Diff", "ref", ref)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("getting diff", "error", err.Error())
		return
	}

	resp := types.RepoCommitResponse{
		Ref:  ref,
		Diff: diff,
	}

	writeJSON(w, resp)
}

func (h *Handle) Tags(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	l := h.l.With("handler", "Refs")

	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	tags, err := gr.Tags()
	if err != nil {
		// Non-fatal, we *should* have at least one branch to show.
		l.Warn("getting tags", "error", err.Error())
	}

	rtags := []*types.TagReference{}
	for _, tag := range tags {
		var target *object.Tag
		if tag.Target != plumbing.ZeroHash {
			target = &tag
		}
		tr := types.TagReference{
			Tag: target,
		}

		tr.Reference = types.Reference{
			Name: tag.Name,
			Hash: tag.Hash.String(),
		}

		if tag.Message != "" {
			tr.Message = tag.Message
		}

		rtags = append(rtags, &tr)
	}

	resp := types.RepoTagsResponse{
		Tags: rtags,
	}

	writeJSON(w, resp)
}

func (h *Handle) Branches(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	branches, _ := gr.Branches()

	resp := types.RepoBranchesResponse{
		Branches: branches,
	}

	writeJSON(w, resp)
}

func (h *Handle) Branch(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	branchName := chi.URLParam(r, "branch")
	branchName, _ = url.PathUnescape(branchName)

	l := h.l.With("handler", "Branch")

	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	ref, err := gr.Branch(branchName)
	if err != nil {
		l.Error("getting branch", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	commit, err := gr.Commit(ref.Hash())
	if err != nil {
		l.Error("getting commit object", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defaultBranch, err := gr.FindMainBranch()
	isDefault := false
	if err != nil {
		l.Error("getting default branch", "error", err.Error())
		// do not quit though
	} else if defaultBranch == branchName {
		isDefault = true
	}

	resp := types.RepoBranchResponse{
		Branch: types.Branch{
			Reference: types.Reference{
				Name: ref.Name().Short(),
				Hash: ref.Hash().String(),
			},
			Commit:    commit,
			IsDefault: isDefault,
		},
	}

	writeJSON(w, resp)
}

func (h *Handle) Keys(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "Keys")

	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.GetAllPublicKeys()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("getting public keys", "error", err.Error())
			return
		}

		data := make([]map[string]any, 0)
		for _, key := range keys {
			j := key.JSON()
			data = append(data, j)
		}
		writeJSON(w, data)
		return

	case http.MethodPut:
		pk := db.PublicKey{}
		if err := json.NewDecoder(r.Body).Decode(&pk); err != nil {
			writeError(w, "invalid request body", http.StatusBadRequest)
			return
		}

		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pk.Key))
		if err != nil {
			writeError(w, "invalid pubkey", http.StatusBadRequest)
		}

		if err := h.db.AddPublicKey(pk); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("adding public key", "error", err.Error())
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}
}

// func (h *Handle) RepoForkAheadBehind(w http.ResponseWriter, r *http.Request) {
// 	l := h.l.With("handler", "RepoForkSync")
//
// 	data := struct {
// 		Did       string `json:"did"`
// 		Source    string `json:"source"`
// 		Name      string `json:"name,omitempty"`
// 		HiddenRef string `json:"hiddenref"`
// 	}{}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, "invalid request body", http.StatusBadRequest)
// 		return
// 	}
//
// 	did := data.Did
// 	source := data.Source
//
// 	if did == "" || source == "" {
// 		l.Error("invalid request body, empty did or name")
// 		w.WriteHeader(http.StatusBadRequest)
// 		return
// 	}
//
// 	var name string
// 	if data.Name != "" {
// 		name = data.Name
// 	} else {
// 		name = filepath.Base(source)
// 	}
//
// 	branch := chi.URLParam(r, "branch")
// 	branch, _ = url.PathUnescape(branch)
//
// 	relativeRepoPath := filepath.Join(did, name)
// 	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
//
// 	gr, err := git.PlainOpen(repoPath)
// 	if err != nil {
// 		log.Println(err)
// 		notFound(w)
// 		return
// 	}
//
// 	forkCommit, err := gr.ResolveRevision(branch)
// 	if err != nil {
// 		l.Error("error resolving ref revision", "msg", err.Error())
// 		writeError(w, fmt.Sprintf("error resolving revision %s", branch), http.StatusBadRequest)
// 		return
// 	}
//
// 	sourceCommit, err := gr.ResolveRevision(data.HiddenRef)
// 	if err != nil {
// 		l.Error("error resolving hidden ref revision", "msg", err.Error())
// 		writeError(w, fmt.Sprintf("error resolving revision %s", data.HiddenRef), http.StatusBadRequest)
// 		return
// 	}
//
// 	status := types.UpToDate
// 	if forkCommit.Hash.String() != sourceCommit.Hash.String() {
// 		isAncestor, err := forkCommit.IsAncestor(sourceCommit)
// 		if err != nil {
// 			log.Printf("error resolving whether %s is ancestor of %s: %s", branch, data.HiddenRef, err)
// 			return
// 		}
//
// 		if isAncestor {
// 			status = types.FastForwardable
// 		} else {
// 			status = types.Conflict
// 		}
// 	}
//
// 	w.Header().Set("Content-Type", "application/json")
// 	json.NewEncoder(w).Encode(types.AncestorCheckResponse{Status: status})
// }

func (h *Handle) RepoLanguages(w http.ResponseWriter, r *http.Request) {
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "RepoLanguages")

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		l.Error("opening repo", "error", err.Error())
		notFound(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	sizes, err := gr.AnalyzeLanguages(ctx)
	if err != nil {
		l.Error("failed to analyze languages", "error", err.Error())
		writeError(w, err.Error(), http.StatusNoContent)
		return
	}

	resp := types.RepoLanguageResponse{Languages: sizes}

	writeJSON(w, resp)
}

// func (h *Handle) RepoForkSync(w http.ResponseWriter, r *http.Request) {
// 	l := h.l.With("handler", "RepoForkSync")
//
// 	data := struct {
// 		Did    string `json:"did"`
// 		Source string `json:"source"`
// 		Name   string `json:"name,omitempty"`
// 	}{}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, "invalid request body", http.StatusBadRequest)
// 		return
// 	}
//
// 	did := data.Did
// 	source := data.Source
//
// 	if did == "" || source == "" {
// 		l.Error("invalid request body, empty did or name")
// 		w.WriteHeader(http.StatusBadRequest)
// 		return
// 	}
//
// 	var name string
// 	if data.Name != "" {
// 		name = data.Name
// 	} else {
// 		name = filepath.Base(source)
// 	}
//
// 	branch := chi.URLParam(r, "branch")
// 	branch, _ = url.PathUnescape(branch)
//
// 	relativeRepoPath := filepath.Join(did, name)
// 	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
//
// 	gr, err := git.Open(repoPath, branch)
// 	if err != nil {
// 		log.Println(err)
// 		notFound(w)
// 		return
// 	}
//
// 	err = gr.Sync()
// 	if err != nil {
// 		l.Error("error syncing repo fork", "error", err.Error())
// 		writeError(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
//
// 	w.WriteHeader(http.StatusNoContent)
// }

// func (h *Handle) RepoFork(w http.ResponseWriter, r *http.Request) {
// 	l := h.l.With("handler", "RepoFork")
//
// 	data := struct {
// 		Did    string `json:"did"`
// 		Source string `json:"source"`
// 		Name   string `json:"name,omitempty"`
// 	}{}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, "invalid request body", http.StatusBadRequest)
// 		return
// 	}
//
// 	did := data.Did
// 	source := data.Source
//
// 	if did == "" || source == "" {
// 		l.Error("invalid request body, empty did or name")
// 		w.WriteHeader(http.StatusBadRequest)
// 		return
// 	}
//
// 	var name string
// 	if data.Name != "" {
// 		name = data.Name
// 	} else {
// 		name = filepath.Base(source)
// 	}
//
// 	relativeRepoPath := filepath.Join(did, name)
// 	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
//
// 	err := git.Fork(repoPath, source)
// 	if err != nil {
// 		l.Error("forking repo", "error", err.Error())
// 		writeError(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
//
// 	// add perms for this user to access the repo
// 	err = h.e.AddRepo(did, rbac.ThisServer, relativeRepoPath)
// 	if err != nil {
// 		l.Error("adding repo permissions", "error", err.Error())
// 		writeError(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
//
// 	hook.SetupRepo(
// 		hook.Config(
// 			hook.WithScanPath(h.c.Repo.ScanPath),
// 			hook.WithInternalApi(h.c.Server.InternalListenAddr),
// 		),
// 		repoPath,
// 	)
//
// 	w.WriteHeader(http.StatusNoContent)
// }

// func (h *Handle) RemoveRepo(w http.ResponseWriter, r *http.Request) {
// 	l := h.l.With("handler", "RemoveRepo")
//
// 	data := struct {
// 		Did  string `json:"did"`
// 		Name string `json:"name"`
// 	}{}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, "invalid request body", http.StatusBadRequest)
// 		return
// 	}
//
// 	did := data.Did
// 	name := data.Name
//
// 	if did == "" || name == "" {
// 		l.Error("invalid request body, empty did or name")
// 		w.WriteHeader(http.StatusBadRequest)
// 		return
// 	}
//
// 	relativeRepoPath := filepath.Join(did, name)
// 	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
// 	err := os.RemoveAll(repoPath)
// 	if err != nil {
// 		l.Error("removing repo", "error", err.Error())
// 		writeError(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
//
// 	w.WriteHeader(http.StatusNoContent)
//
// }

// func (h *Handle) Merge(w http.ResponseWriter, r *http.Request) {
// 	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
//
// 	data := types.MergeRequest{}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, err.Error(), http.StatusBadRequest)
// 		h.l.Error("git: failed to unmarshal json patch", "handler", "Merge", "error", err)
// 		return
// 	}
//
// 	mo := &git.MergeOptions{
// 		AuthorName:    data.AuthorName,
// 		AuthorEmail:   data.AuthorEmail,
// 		CommitBody:    data.CommitBody,
// 		CommitMessage: data.CommitMessage,
// 	}
//
// 	patch := data.Patch
// 	branch := data.Branch
// 	gr, err := git.Open(path, branch)
// 	if err != nil {
// 		notFound(w)
// 		return
// 	}
//
// 	mo.FormatPatch = patchutil.IsFormatPatch(patch)
//
// 	if err := gr.MergeWithOptions([]byte(patch), branch, mo); err != nil {
// 		var mergeErr *git.ErrMerge
// 		if errors.As(err, &mergeErr) {
// 			conflicts := make([]types.ConflictInfo, len(mergeErr.Conflicts))
// 			for i, conflict := range mergeErr.Conflicts {
// 				conflicts[i] = types.ConflictInfo{
// 					Filename: conflict.Filename,
// 					Reason:   conflict.Reason,
// 				}
// 			}
// 			response := types.MergeCheckResponse{
// 				IsConflicted: true,
// 				Conflicts:    conflicts,
// 				Message:      mergeErr.Message,
// 			}
// 			writeConflict(w, response)
// 			h.l.Error("git: merge conflict", "handler", "Merge", "error", mergeErr)
// 		} else {
// 			writeError(w, err.Error(), http.StatusBadRequest)
// 			h.l.Error("git: failed to merge", "handler", "Merge", "error", err.Error())
// 		}
// 		return
// 	}
//
// 	w.WriteHeader(http.StatusOK)
// }

// func (h *Handle) MergeCheck(w http.ResponseWriter, r *http.Request) {
// 	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
//
// 	var data struct {
// 		Patch  string `json:"patch"`
// 		Branch string `json:"branch"`
// 	}
//
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		writeError(w, err.Error(), http.StatusBadRequest)
// 		h.l.Error("git: failed to unmarshal json patch", "handler", "MergeCheck", "error", err)
// 		return
// 	}
//
// 	patch := data.Patch
// 	branch := data.Branch
// 	gr, err := git.Open(path, branch)
// 	if err != nil {
// 		notFound(w)
// 		return
// 	}
//
// 	err = gr.MergeCheck([]byte(patch), branch)
// 	if err == nil {
// 		response := types.MergeCheckResponse{
// 			IsConflicted: false,
// 		}
// 		writeJSON(w, response)
// 		return
// 	}
//
// 	var mergeErr *git.ErrMerge
// 	if errors.As(err, &mergeErr) {
// 		conflicts := make([]types.ConflictInfo, len(mergeErr.Conflicts))
// 		for i, conflict := range mergeErr.Conflicts {
// 			conflicts[i] = types.ConflictInfo{
// 				Filename: conflict.Filename,
// 				Reason:   conflict.Reason,
// 			}
// 		}
// 		response := types.MergeCheckResponse{
// 			IsConflicted: true,
// 			Conflicts:    conflicts,
// 			Message:      mergeErr.Message,
// 		}
// 		writeConflict(w, response)
// 		h.l.Error("git: merge conflict", "handler", "MergeCheck", "error", mergeErr.Error())
// 		return
// 	}
// 	writeError(w, err.Error(), http.StatusInternalServerError)
// 	h.l.Error("git: failed to check merge", "handler", "MergeCheck", "error", err.Error())
// }

func (h *Handle) Compare(w http.ResponseWriter, r *http.Request) {
	rev1 := chi.URLParam(r, "rev1")
	rev1, _ = url.PathUnescape(rev1)

	rev2 := chi.URLParam(r, "rev2")
	rev2, _ = url.PathUnescape(rev2)

	l := h.l.With("handler", "Compare", "r1", rev1, "r2", rev2)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	commit1, err := gr.ResolveRevision(rev1)
	if err != nil {
		l.Error("error resolving revision 1", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", rev1), http.StatusBadRequest)
		return
	}

	commit2, err := gr.ResolveRevision(rev2)
	if err != nil {
		l.Error("error resolving revision 2", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", rev2), http.StatusBadRequest)
		return
	}

	rawPatch, formatPatch, err := gr.FormatPatch(commit1, commit2)
	if err != nil {
		l.Error("error comparing revisions", "msg", err.Error())
		writeError(w, "error comparing revisions", http.StatusBadRequest)
		return
	}

	writeJSON(w, types.RepoFormatPatchResponse{
		Rev1:        commit1.Hash.String(),
		Rev2:        commit2.Hash.String(),
		FormatPatch: formatPatch,
		Patch:       rawPatch,
	})
}

func (h *Handle) DefaultBranch(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "DefaultBranch")
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	branch, err := gr.FindMainBranch()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("getting default branch", "error", err.Error())
		return
	}

	writeJSON(w, types.RepoDefaultBranchResponse{
		Branch: branch,
	})
}
