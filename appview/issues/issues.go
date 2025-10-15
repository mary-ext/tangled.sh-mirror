package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	atpclient "github.com/bluesky-social/indigo/atproto/client"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pagination"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/validator"
	"tangled.org/core/idresolver"
	"tangled.org/core/tid"
)

type Issues struct {
	oauth        *oauth.OAuth
	repoResolver *reporesolver.RepoResolver
	pages        *pages.Pages
	idResolver   *idresolver.Resolver
	db           *db.DB
	config       *config.Config
	notifier     notify.Notifier
	logger       *slog.Logger
	validator    *validator.Validator
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	notifier notify.Notifier,
	validator *validator.Validator,
	logger *slog.Logger,
) *Issues {
	return &Issues{
		oauth:        oauth,
		repoResolver: repoResolver,
		pages:        pages,
		idResolver:   idResolver,
		db:           db,
		config:       config,
		notifier:     notifier,
		logger:       logger,
		validator:    validator,
	}
}

func (rp *Issues) RepoSingleIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoSingleIssue")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	reactionMap, err := db.GetReactionMap(rp.db, 20, issue.AtUri())
	if err != nil {
		l.Error("failed to get issue reactions", "err", err)
	}

	userReactions := map[models.ReactionKind]bool{}
	if user != nil {
		userReactions = db.GetReactionStatusMap(rp.db, user.Did, issue.AtUri())
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", tangled.RepoIssueNSID),
	)
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	rp.pages.RepoSingleIssue(w, pages.RepoSingleIssueParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(user),
		Issue:                issue,
		CommentList:          issue.CommentList(),
		OrderedReactionKinds: models.OrderedReactionKinds,
		Reactions:            reactionMap,
		UserReacted:          userReactions,
		LabelDefs:            defs,
	})
}

func (rp *Issues) EditIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "EditIssue")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rp.pages.EditIssueFragment(w, pages.EditIssueParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Issue:        issue,
		})
	case http.MethodPost:
		noticeId := "issues"
		newIssue := issue
		newIssue.Title = r.FormValue("title")
		newIssue.Body = r.FormValue("body")

		if err := rp.validator.ValidateIssue(newIssue); err != nil {
			l.Error("validation error", "err", err)
			rp.pages.Notice(w, noticeId, fmt.Sprintf("Failed to edit issue: %s", err))
			return
		}

		newRecord := newIssue.AsRecord()

		// edit an atproto record
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to get authorized client", "err", err)
			rp.pages.Notice(w, noticeId, "Failed to edit issue.")
			return
		}

		ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoIssueNSID, user.Did, newIssue.Rkey)
		if err != nil {
			l.Error("failed to get record", "err", err)
			rp.pages.Notice(w, noticeId, "Failed to edit issue, no record found on PDS.")
			return
		}

		_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueNSID,
			Repo:       user.Did,
			Rkey:       newIssue.Rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &newRecord,
			},
		})
		if err != nil {
			l.Error("failed to edit record on PDS", "err", err)
			rp.pages.Notice(w, noticeId, "Failed to edit issue on PDS.")
			return
		}

		// modify on DB -- TODO: transact this cleverly
		tx, err := rp.db.Begin()
		if err != nil {
			l.Error("failed to edit issue on DB", "err", err)
			rp.pages.Notice(w, noticeId, "Failed to edit issue.")
			return
		}
		defer tx.Rollback()

		err = db.PutIssue(tx, newIssue)
		if err != nil {
			l.Error("failed to edit issue", "err", err)
			rp.pages.Notice(w, "issues", "Failed to edit issue.")
			return
		}

		if err = tx.Commit(); err != nil {
			l.Error("failed to edit issue", "err", err)
			rp.pages.Notice(w, "issues", "Failed to cedit issue.")
			return
		}

		rp.pages.HxRefresh(w)
	}
}

func (rp *Issues) DeleteIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DeleteIssue")
	noticeId := "issue-actions-error"

	user := rp.oauth.GetUser(r)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Notice(w, noticeId, "Failed to delete issue.")
		return
	}
	l = l.With("did", issue.Did, "rkey", issue.Rkey)

	// delete from PDS
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to get authorized client", "err", err)
		rp.pages.Notice(w, "issue-comment", "Failed to delete comment.")
		return
	}
	_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoIssueNSID,
		Repo:       issue.Did,
		Rkey:       issue.Rkey,
	})
	if err != nil {
		// TODO: transact this better
		l.Error("failed to delete issue from PDS", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to delete issue.")
		return
	}

	// delete from db
	if err := db.DeleteIssues(rp.db, db.FilterEq("id", issue.Id)); err != nil {
		l.Error("failed to delete issue", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to delete issue.")
		return
	}

	// return to all issues page
	rp.pages.HxRedirect(w, "/"+f.RepoInfo(user).FullName()+"/issues")
}

func (rp *Issues) CloseIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "CloseIssue")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	collaborators, err := f.Collaborators(r.Context())
	if err != nil {
		l.Error("failed to fetch repo collaborators", "err", err)
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.Did

	// TODO: make this more granular
	if isIssueOwner || isCollaborator {
		err = db.CloseIssues(
			rp.db,
			db.FilterEq("id", issue.Id),
		)
		if err != nil {
			l.Error("failed to close issue", "err", err)
			rp.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		// notify about the issue closure
		rp.notifier.NewIssueClosed(r.Context(), issue)

		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issue.IssueId))
		return
	} else {
		l.Error("user is not permitted to close issue")
		http.Error(w, "for biden", http.StatusUnauthorized)
		return
	}
}

func (rp *Issues) ReopenIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "ReopenIssue")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	collaborators, err := f.Collaborators(r.Context())
	if err != nil {
		l.Error("failed to fetch repo collaborators", "err", err)
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.Did

	if isCollaborator || isIssueOwner {
		err := db.ReopenIssues(
			rp.db,
			db.FilterEq("id", issue.Id),
		)
		if err != nil {
			l.Error("failed to reopen issue", "err", err)
			rp.pages.Notice(w, "issue-action", "Failed to reopen issue. Try again later.")
			return
		}
		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issue.IssueId))
		return
	} else {
		l.Error("user is not the owner of the repo")
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
}

func (rp *Issues) NewIssueComment(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "NewIssueComment")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	body := r.FormValue("body")
	if body == "" {
		rp.pages.Notice(w, "issue", "Body is required")
		return
	}

	replyToUri := r.FormValue("reply-to")
	var replyTo *string
	if replyToUri != "" {
		replyTo = &replyToUri
	}

	comment := models.IssueComment{
		Did:     user.Did,
		Rkey:    tid.TID(),
		IssueAt: issue.AtUri().String(),
		ReplyTo: replyTo,
		Body:    body,
		Created: time.Now(),
	}
	if err = rp.validator.ValidateIssueComment(&comment); err != nil {
		l.Error("failed to validate comment", "err", err)
		rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
		return
	}
	record := comment.AsRecord()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to get authorized client", "err", err)
		rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
		return
	}

	// create a record first
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoIssueCommentNSID,
		Repo:       comment.Did,
		Rkey:       comment.Rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})
	if err != nil {
		l.Error("failed to create comment", "err", err)
		rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
		return
	}
	atUri := resp.Uri
	defer func() {
		if err := rollbackRecord(context.Background(), atUri, client); err != nil {
			l.Error("rollback failed", "err", err)
		}
	}()

	commentId, err := db.AddIssueComment(rp.db, comment)
	if err != nil {
		l.Error("failed to create comment", "err", err)
		rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
		return
	}

	// reset atUri to make rollback a no-op
	atUri = ""

	// notify about the new comment
	comment.Id = commentId
	rp.notifier.NewIssueComment(r.Context(), &comment)

	rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d#comment-%d", f.OwnerSlashRepo(), issue.IssueId, commentId))
}

func (rp *Issues) IssueComment(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "IssueComment")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	commentId := chi.URLParam(r, "commentId")
	comments, err := db.GetIssueComments(
		rp.db,
		db.FilterEq("id", commentId),
	)
	if err != nil {
		l.Error("failed to fetch comment", "id", commentId)
		http.Error(w, "failed to fetch comment id", http.StatusBadRequest)
		return
	}
	if len(comments) != 1 {
		l.Error("incorrect number of comments returned", "id", commentId, "len(comments)", len(comments))
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	comment := comments[0]

	rp.pages.IssueCommentBodyFragment(w, pages.IssueCommentBodyParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      &comment,
	})
}

func (rp *Issues) EditIssueComment(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "EditIssueComment")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	commentId := chi.URLParam(r, "commentId")
	comments, err := db.GetIssueComments(
		rp.db,
		db.FilterEq("id", commentId),
	)
	if err != nil {
		l.Error("failed to fetch comment", "id", commentId)
		http.Error(w, "failed to fetch comment id", http.StatusBadRequest)
		return
	}
	if len(comments) != 1 {
		l.Error("incorrect number of comments returned", "id", commentId, "len(comments)", len(comments))
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	comment := comments[0]

	if comment.Did != user.Did {
		l.Error("unauthorized comment edit", "expectedDid", comment.Did, "gotDid", user.Did)
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rp.pages.EditIssueCommentFragment(w, pages.EditIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Issue:        issue,
			Comment:      &comment,
		})
	case http.MethodPost:
		// extract form value
		newBody := r.FormValue("body")
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to get authorized client", "err", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		now := time.Now()
		newComment := comment
		newComment.Body = newBody
		newComment.Edited = &now
		record := newComment.AsRecord()

		_, err = db.AddIssueComment(rp.db, newComment)
		if err != nil {
			l.Error("failed to perferom update-description query", "err", err)
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// rkey is optional, it was introduced later
		if newComment.Rkey != "" {
			// update the record on pds
			ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoIssueCommentNSID, user.Did, comment.Rkey)
			if err != nil {
				l.Error("failed to get record", "err", err, "did", newComment.Did, "rkey", newComment.Rkey)
				rp.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "Failed to update description, no record found on PDS.")
				return
			}

			_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
				Collection: tangled.RepoIssueCommentNSID,
				Repo:       user.Did,
				Rkey:       newComment.Rkey,
				SwapRecord: ex.Cid,
				Record: &lexutil.LexiconTypeDecoder{
					Val: &record,
				},
			})
			if err != nil {
				l.Error("failed to update record on PDS", "err", err)
			}
		}

		// return new comment body with htmx
		rp.pages.IssueCommentBodyFragment(w, pages.IssueCommentBodyParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Issue:        issue,
			Comment:      &newComment,
		})
	}
}

func (rp *Issues) ReplyIssueCommentPlaceholder(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "ReplyIssueCommentPlaceholder")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	commentId := chi.URLParam(r, "commentId")
	comments, err := db.GetIssueComments(
		rp.db,
		db.FilterEq("id", commentId),
	)
	if err != nil {
		l.Error("failed to fetch comment", "id", commentId)
		http.Error(w, "failed to fetch comment id", http.StatusBadRequest)
		return
	}
	if len(comments) != 1 {
		l.Error("incorrect number of comments returned", "id", commentId, "len(comments)", len(comments))
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	comment := comments[0]

	rp.pages.ReplyIssueCommentPlaceholderFragment(w, pages.ReplyIssueCommentPlaceholderParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      &comment,
	})
}

func (rp *Issues) ReplyIssueComment(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "ReplyIssueComment")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	commentId := chi.URLParam(r, "commentId")
	comments, err := db.GetIssueComments(
		rp.db,
		db.FilterEq("id", commentId),
	)
	if err != nil {
		l.Error("failed to fetch comment", "id", commentId)
		http.Error(w, "failed to fetch comment id", http.StatusBadRequest)
		return
	}
	if len(comments) != 1 {
		l.Error("incorrect number of comments returned", "id", commentId, "len(comments)", len(comments))
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	comment := comments[0]

	rp.pages.ReplyIssueCommentFragment(w, pages.ReplyIssueCommentParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      &comment,
	})
}

func (rp *Issues) DeleteIssueComment(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DeleteIssueComment")
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		l.Error("failed to get issue")
		rp.pages.Error404(w)
		return
	}

	commentId := chi.URLParam(r, "commentId")
	comments, err := db.GetIssueComments(
		rp.db,
		db.FilterEq("id", commentId),
	)
	if err != nil {
		l.Error("failed to fetch comment", "id", commentId)
		http.Error(w, "failed to fetch comment id", http.StatusBadRequest)
		return
	}
	if len(comments) != 1 {
		l.Error("incorrect number of comments returned", "id", commentId, "len(comments)", len(comments))
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	comment := comments[0]

	if comment.Did != user.Did {
		l.Error("unauthorized action", "expectedDid", comment.Did, "gotDid", user.Did)
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	if comment.Deleted != nil {
		http.Error(w, "comment already deleted", http.StatusBadRequest)
		return
	}

	// optimistic deletion
	deleted := time.Now()
	err = db.DeleteIssueComments(rp.db, db.FilterEq("id", comment.Id))
	if err != nil {
		l.Error("failed to delete comment", "err", err)
		rp.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "failed to delete comment")
		return
	}

	// delete from pds
	if comment.Rkey != "" {
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to get authorized client", "err", err)
			rp.pages.Notice(w, "issue-comment", "Failed to delete comment.")
			return
		}
		_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.RepoIssueCommentNSID,
			Repo:       user.Did,
			Rkey:       comment.Rkey,
		})
		if err != nil {
			l.Error("failed to delete from PDS", "err", err)
		}
	}

	// optimistic update for htmx
	comment.Body = ""
	comment.Deleted = &deleted

	// htmx fragment of comment after deletion
	rp.pages.IssueCommentBodyFragment(w, pages.IssueCommentBodyParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      &comment,
	})
}

func (rp *Issues) RepoIssues(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoIssues")

	params := r.URL.Query()
	state := params.Get("state")
	isOpen := true
	switch state {
	case "open":
		isOpen = true
	case "closed":
		isOpen = false
	default:
		isOpen = true
	}

	page, ok := r.Context().Value("page").(pagination.Page)
	if !ok {
		l.Error("failed to get page")
		page = pagination.FirstPage()
	}

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	openVal := 0
	if isOpen {
		openVal = 1
	}
	issues, err := db.GetIssuesPaginated(
		rp.db,
		page,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("open", openVal),
	)
	if err != nil {
		l.Error("failed to get issues", "err", err)
		rp.pages.Notice(w, "issues", "Failed to load issues. Try again later.")
		return
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", tangled.RepoIssueNSID),
	)
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	rp.pages.RepoIssues(w, pages.RepoIssuesParams{
		LoggedInUser:    rp.oauth.GetUser(r),
		RepoInfo:        f.RepoInfo(user),
		Issues:          issues,
		LabelDefs:       defs,
		FilteringByOpen: isOpen,
		Page:            page,
	})
}

func (rp *Issues) NewIssue(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "NewIssue")
	user := rp.oauth.GetUser(r)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rp.pages.RepoNewIssue(w, pages.RepoNewIssueParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
		})
	case http.MethodPost:
		issue := &models.Issue{
			RepoAt:  f.RepoAt(),
			Rkey:    tid.TID(),
			Title:   r.FormValue("title"),
			Body:    r.FormValue("body"),
			Did:     user.Did,
			Created: time.Now(),
			Repo:    &f.Repo,
		}

		if err := rp.validator.ValidateIssue(issue); err != nil {
			l.Error("validation error", "err", err)
			rp.pages.Notice(w, "issues", fmt.Sprintf("Failed to create issue: %s", err))
			return
		}

		record := issue.AsRecord()

		// create an atproto record
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to get authorized client", "err", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueNSID,
			Repo:       user.Did,
			Rkey:       issue.Rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			l.Error("failed to create issue", "err", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}
		atUri := resp.Uri

		tx, err := rp.db.BeginTx(r.Context(), nil)
		if err != nil {
			rp.pages.Notice(w, "issues", "Failed to create issue, try again later")
			return
		}
		rollback := func() {
			err1 := tx.Rollback()
			err2 := rollbackRecord(context.Background(), atUri, client)

			if errors.Is(err1, sql.ErrTxDone) {
				err1 = nil
			}

			if err := errors.Join(err1, err2); err != nil {
				l.Error("failed to rollback txn", "err", err)
			}
		}
		defer rollback()

		err = db.PutIssue(tx, issue)
		if err != nil {
			l.Error("failed to create issue", "err", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		if err = tx.Commit(); err != nil {
			l.Error("failed to create issue", "err", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		// everything is successful, do not rollback the atproto record
		atUri = ""
		rp.notifier.NewIssue(r.Context(), issue)
		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issue.IssueId))
		return
	}
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, client *atpclient.APIClient) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}
