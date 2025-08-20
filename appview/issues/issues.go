package issues

import (
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/data"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/notify"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/tid"
)

type Issues struct {
	oauth        *oauth.OAuth
	repoResolver *reporesolver.RepoResolver
	pages        *pages.Pages
	idResolver   *idresolver.Resolver
	db           *db.DB
	config       *config.Config
	notifier     notify.Notifier
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	notifier notify.Notifier,
) *Issues {
	return &Issues{
		oauth:        oauth,
		repoResolver: repoResolver,
		pages:        pages,
		idResolver:   idResolver,
		db:           db,
		config:       config,
		notifier:     notifier,
	}
}

func (rp *Issues) RepoSingleIssue(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, comments, err := db.GetIssueWithComments(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue and comments", err)
		rp.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	reactionCountMap, err := db.GetReactionCountMap(rp.db, issue.AtUri())
	if err != nil {
		log.Println("failed to get issue reactions")
		rp.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
	}

	userReactions := map[db.ReactionKind]bool{}
	if user != nil {
		userReactions = db.GetReactionStatusMap(rp.db, user.Did, issue.AtUri())
	}

	issueOwnerIdent, err := rp.idResolver.ResolveIdent(r.Context(), issue.OwnerDid)
	if err != nil {
		log.Println("failed to resolve issue owner", err)
	}

	rp.pages.RepoSingleIssue(w, pages.RepoSingleIssueParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comments:     comments,

		IssueOwnerHandle: issueOwnerIdent.Handle.String(),

		OrderedReactionKinds: db.OrderedReactionKinds,
		Reactions:            reactionCountMap,
		UserReacted:          userReactions,
	})

}

func (rp *Issues) CloseIssue(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		rp.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
		return
	}

	collaborators, err := f.Collaborators(r.Context())
	if err != nil {
		log.Println("failed to fetch repo collaborators: %w", err)
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.OwnerDid

	// TODO: make this more granular
	if isIssueOwner || isCollaborator {

		closed := tangled.RepoIssueStateClosed

		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			return
		}
		_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueStateNSID,
			Repo:       user.Did,
			Rkey:       tid.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueState{
					Issue: issue.AtUri().String(),
					State: closed,
				},
			},
		})

		if err != nil {
			log.Println("failed to update issue state", err)
			rp.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		err = db.CloseIssue(rp.db, f.RepoAt(), issueIdInt)
		if err != nil {
			log.Println("failed to close issue", err)
			rp.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not permitted to close issue")
		http.Error(w, "for biden", http.StatusUnauthorized)
		return
	}
}

func (rp *Issues) ReopenIssue(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		rp.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
		return
	}

	collaborators, err := f.Collaborators(r.Context())
	if err != nil {
		log.Println("failed to fetch repo collaborators: %w", err)
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.OwnerDid

	if isCollaborator || isIssueOwner {
		err := db.ReopenIssue(rp.db, f.RepoAt(), issueIdInt)
		if err != nil {
			log.Println("failed to reopen issue", err)
			rp.pages.Notice(w, "issue-action", "Failed to reopen issue. Try again later.")
			return
		}
		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not the owner of the repo")
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
}

func (rp *Issues) NewIssueComment(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		body := r.FormValue("body")
		if body == "" {
			rp.pages.Notice(w, "issue", "Body is required")
			return
		}

		commentId := mathrand.IntN(1000000)
		rkey := tid.TID()

		err := db.NewIssueComment(rp.db, &db.Comment{
			OwnerDid:  user.Did,
			RepoAt:    f.RepoAt(),
			Issue:     issueIdInt,
			CommentId: commentId,
			Body:      body,
			Rkey:      rkey,
		})
		if err != nil {
			log.Println("failed to create comment", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		createdAt := time.Now().Format(time.RFC3339)
		ownerDid := user.Did
		issueAt, err := db.GetIssueAt(rp.db, f.RepoAt(), issueIdInt)
		if err != nil {
			log.Println("failed to get issue at", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		atUri := f.RepoAt().String()
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}
		_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueCommentNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueComment{
					Repo:      &atUri,
					Issue:     issueAt,
					Owner:     &ownerDid,
					Body:      body,
					CreatedAt: createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create comment", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d#comment-%d", f.OwnerSlashRepo(), issueIdInt, commentId))
		return
	}
}

func (rp *Issues) IssueComment(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		rp.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(rp.db, f.RepoAt(), issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	rp.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      comment,
	})
}

func (rp *Issues) EditIssueComment(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		rp.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(rp.db, f.RepoAt(), issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rp.pages.EditIssueCommentFragment(w, pages.EditIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Issue:        issue,
			Comment:      comment,
		})
	case http.MethodPost:
		// extract form value
		newBody := r.FormValue("body")
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			rp.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}
		rkey := comment.Rkey

		// optimistic update
		edited := time.Now()
		err = db.EditComment(rp.db, comment.RepoAt, comment.Issue, comment.CommentId, newBody)
		if err != nil {
			log.Println("failed to perferom update-description query", err)
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// rkey is optional, it was introduced later
		if comment.Rkey != "" {
			// update the record on pds
			ex, err := client.RepoGetRecord(r.Context(), "", tangled.RepoIssueCommentNSID, user.Did, rkey)
			if err != nil {
				// failed to get record
				log.Println(err, rkey)
				rp.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "Failed to update description, no record found on PDS.")
				return
			}
			value, _ := ex.Value.MarshalJSON() // we just did get record; it is valid json
			record, _ := data.UnmarshalJSON(value)

			repoAt := record["repo"].(string)
			issueAt := record["issue"].(string)
			createdAt := record["createdAt"].(string)

			_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
				Collection: tangled.RepoIssueCommentNSID,
				Repo:       user.Did,
				Rkey:       rkey,
				SwapRecord: ex.Cid,
				Record: &lexutil.LexiconTypeDecoder{
					Val: &tangled.RepoIssueComment{
						Repo:      &repoAt,
						Issue:     issueAt,
						Owner:     &comment.OwnerDid,
						Body:      newBody,
						CreatedAt: createdAt,
					},
				},
			})
			if err != nil {
				log.Println(err)
			}
		}

		// optimistic update for htmx
		comment.Body = newBody
		comment.Edited = &edited

		// return new comment body with htmx
		rp.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Issue:        issue,
			Comment:      comment,
		})
		return

	}

}

func (rp *Issues) DeleteIssueComment(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(rp.db, f.RepoAt(), issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		rp.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	comment, err := db.GetComment(rp.db, f.RepoAt(), issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	if comment.Deleted != nil {
		http.Error(w, "comment already deleted", http.StatusBadRequest)
		return
	}

	// optimistic deletion
	deleted := time.Now()
	err = db.DeleteComment(rp.db, f.RepoAt(), issueIdInt, commentIdInt)
	if err != nil {
		log.Println("failed to delete comment")
		rp.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "failed to delete comment")
		return
	}

	// delete from pds
	if comment.Rkey != "" {
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			rp.pages.Notice(w, "issue-comment", "Failed to delete comment.")
			return
		}
		_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       user.Did,
			Rkey:       comment.Rkey,
		})
		if err != nil {
			log.Println(err)
		}
	}

	// optimistic update for htmx
	comment.Body = ""
	comment.Deleted = &deleted

	// htmx fragment of comment after deletion
	rp.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Issue:        issue,
		Comment:      comment,
	})
}

func (rp *Issues) RepoIssues(w http.ResponseWriter, r *http.Request) {
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
		log.Println("failed to get page")
		page = pagination.FirstPage()
	}

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issues, err := db.GetIssuesPaginated(rp.db, f.RepoAt(), isOpen, page)
	if err != nil {
		log.Println("failed to get issues", err)
		rp.pages.Notice(w, "issues", "Failed to load issues. Try again later.")
		return
	}

	rp.pages.RepoIssues(w, pages.RepoIssuesParams{
		LoggedInUser:    rp.oauth.GetUser(r),
		RepoInfo:        f.RepoInfo(user),
		Issues:          issues,
		FilteringByOpen: isOpen,
		Page:            page,
	})
}

func (rp *Issues) NewIssue(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rp.pages.RepoNewIssue(w, pages.RepoNewIssueParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
		})
	case http.MethodPost:
		title := r.FormValue("title")
		body := r.FormValue("body")

		if title == "" || body == "" {
			rp.pages.Notice(w, "issues", "Title and body are required")
			return
		}

		sanitizer := markup.NewSanitizer()
		if st := strings.TrimSpace(sanitizer.SanitizeDescription(title)); st == "" {
			rp.pages.Notice(w, "issues", "Title is empty after HTML sanitization")
			return
		}
		if sb := strings.TrimSpace(sanitizer.SanitizeDefault(body)); sb == "" {
			rp.pages.Notice(w, "issues", "Body is empty after HTML sanitization")
			return
		}

		tx, err := rp.db.BeginTx(r.Context(), nil)
		if err != nil {
			rp.pages.Notice(w, "issues", "Failed to create issue, try again later")
			return
		}

		issue := &db.Issue{
			RepoAt:   f.RepoAt(),
			Rkey:     tid.TID(),
			Title:    title,
			Body:     body,
			OwnerDid: user.Did,
		}
		err = db.NewIssue(tx, issue)
		if err != nil {
			log.Println("failed to create issue", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}
		atUri := f.RepoAt().String()
		_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueNSID,
			Repo:       user.Did,
			Rkey:       issue.Rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssue{
					Repo:  atUri,
					Title: title,
					Body:  &body,
					Owner: user.Did,
				},
			},
		})
		if err != nil {
			log.Println("failed to create issue", err)
			rp.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		rp.notifier.NewIssue(r.Context(), issue)

		rp.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issue.IssueId))
		return
	}
}
