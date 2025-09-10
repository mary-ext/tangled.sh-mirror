package strings

import (
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/notify"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/tid"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
)

type Strings struct {
	Db         *db.DB
	OAuth      *oauth.OAuth
	Pages      *pages.Pages
	IdResolver *idresolver.Resolver
	Logger     *slog.Logger
	Notifier   notify.Notifier
}

func (s *Strings) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.
		Get("/", s.timeline)

	r.
		With(mw.ResolveIdent()).
		Route("/{user}", func(r chi.Router) {
			r.Get("/", s.dashboard)

			r.Route("/{rkey}", func(r chi.Router) {
				r.Get("/", s.contents)
				r.Delete("/", s.delete)
				r.Get("/raw", s.contents)
				r.Get("/edit", s.edit)
				r.Post("/edit", s.edit)
				r.
					With(middleware.AuthMiddleware(s.OAuth)).
					Post("/comment", s.comment)
			})
		})

	r.
		With(middleware.AuthMiddleware(s.OAuth)).
		Route("/new", func(r chi.Router) {
			r.Get("/", s.create)
			r.Post("/", s.create)
		})

	return r
}

func (s *Strings) timeline(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "timeline")

	strings, err := db.GetStrings(s.Db, 50)
	if err != nil {
		l.Error("failed to fetch string", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.Pages.StringsTimeline(w, pages.StringTimelineParams{
		LoggedInUser: s.OAuth.GetUser(r),
		Strings:      strings,
	})
}

func (s *Strings) contents(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "contents")

	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		l.Error("malformed middleware")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	l = l.With("did", id.DID, "handle", id.Handle)

	rkey := chi.URLParam(r, "rkey")
	if rkey == "" {
		l.Error("malformed url, empty rkey")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	l = l.With("rkey", rkey)

	strings, err := db.GetStrings(
		s.Db,
		0,
		db.FilterEq("did", id.DID),
		db.FilterEq("rkey", rkey),
	)
	if err != nil {
		l.Error("failed to fetch string", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(strings) < 1 {
		l.Error("string not found")
		s.Pages.Error404(w)
		return
	}
	if len(strings) != 1 {
		l.Error("incorrect number of records returned", "len(strings)", len(strings))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	string := strings[0]

	if path.Base(r.URL.Path) == "raw" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if string.Filename != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", string.Filename))
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(string.Contents)))

		_, err = w.Write([]byte(string.Contents))
		if err != nil {
			l.Error("failed to write raw response", "err", err)
		}
		return
	}

	var showRendered, renderToggle bool
	if markup.GetFormat(string.Filename) == markup.FormatMarkdown {
		renderToggle = true
		showRendered = r.URL.Query().Get("code") != "true"
	}

	s.Pages.SingleString(w, pages.SingleStringParams{
		LoggedInUser: s.OAuth.GetUser(r),
		RenderToggle: renderToggle,
		ShowRendered: showRendered,
		String:       string,
		Stats:        string.Stats(),
		Owner:        id,
	})
}

func (s *Strings) dashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/%s?tab=strings", chi.URLParam(r, "user")), http.StatusFound)
}

func (s *Strings) edit(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "edit")

	user := s.OAuth.GetUser(r)

	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		l.Error("malformed middleware")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	l = l.With("did", id.DID, "handle", id.Handle)

	rkey := chi.URLParam(r, "rkey")
	if rkey == "" {
		l.Error("malformed url, empty rkey")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	l = l.With("rkey", rkey)

	// get the string currently being edited
	all, err := db.GetStrings(
		s.Db,
		0,
		db.FilterEq("did", id.DID),
		db.FilterEq("rkey", rkey),
	)
	if err != nil {
		l.Error("failed to fetch string", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(all) != 1 {
		l.Error("incorrect number of records returned", "len(strings)", len(all))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	first := all[0]

	// verify that the logged in user owns this string
	if user.Did != id.DID.String() {
		l.Error("unauthorized request", "expected", id.DID, "got", user.Did)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// return the form with prefilled fields
		s.Pages.PutString(w, pages.PutStringParams{
			LoggedInUser: s.OAuth.GetUser(r),
			Action:       "edit",
			String:       first,
		})
	case http.MethodPost:
		fail := func(msg string, err error) {
			l.Error(msg, "err", err)
			s.Pages.Notice(w, "error", msg)
		}

		filename := r.FormValue("filename")
		if filename == "" {
			fail("Empty filename.", nil)
			return
		}

		content := r.FormValue("content")
		if content == "" {
			fail("Empty contents.", nil)
			return
		}

		description := r.FormValue("description")

		// construct new string from form values
		entry := db.String{
			Did:         first.Did,
			Rkey:        first.Rkey,
			Filename:    filename,
			Description: description,
			Contents:    content,
			Created:     first.Created,
		}

		record := entry.AsRecord()

		client, err := s.OAuth.AuthorizedClient(r)
		if err != nil {
			fail("Failed to create record.", err)
			return
		}

		// first replace the existing record in the PDS
		ex, err := client.RepoGetRecord(r.Context(), "", tangled.StringNSID, entry.Did.String(), entry.Rkey)
		if err != nil {
			fail("Failed to updated existing record.", err)
			return
		}
		resp, err := client.RepoPutRecord(r.Context(), &atproto.RepoPutRecord_Input{
			Collection: tangled.StringNSID,
			Repo:       entry.Did.String(),
			Rkey:       entry.Rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			fail("Failed to updated existing record.", err)
			return
		}
		l := l.With("aturi", resp.Uri)
		l.Info("edited string")

		// if that went okay, updated the db
		if err = db.AddString(s.Db, entry); err != nil {
			fail("Failed to update string.", err)
			return
		}

		s.Notifier.EditString(r.Context(), &entry)

		// if that went okay, redir to the string
		s.Pages.HxRedirect(w, "/strings/"+user.Handle+"/"+entry.Rkey)
	}

}

func (s *Strings) create(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "create")
	user := s.OAuth.GetUser(r)

	switch r.Method {
	case http.MethodGet:
		s.Pages.PutString(w, pages.PutStringParams{
			LoggedInUser: s.OAuth.GetUser(r),
			Action:       "new",
		})
	case http.MethodPost:
		fail := func(msg string, err error) {
			l.Error(msg, "err", err)
			s.Pages.Notice(w, "error", msg)
		}

		filename := r.FormValue("filename")
		if filename == "" {
			fail("Empty filename.", nil)
			return
		}

		content := r.FormValue("content")
		if content == "" {
			fail("Empty contents.", nil)
			return
		}

		description := r.FormValue("description")

		string := db.String{
			Did:         syntax.DID(user.Did),
			Rkey:        tid.TID(),
			Filename:    filename,
			Description: description,
			Contents:    content,
			Created:     time.Now(),
		}

		record := string.AsRecord()

		client, err := s.OAuth.AuthorizedClient(r)
		if err != nil {
			fail("Failed to create record.", err)
			return
		}

		resp, err := client.RepoPutRecord(r.Context(), &atproto.RepoPutRecord_Input{
			Collection: tangled.StringNSID,
			Repo:       user.Did,
			Rkey:       string.Rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			fail("Failed to create record.", err)
			return
		}
		l := l.With("aturi", resp.Uri)
		l.Info("created record")

		// insert into DB
		if err = db.AddString(s.Db, string); err != nil {
			fail("Failed to create string.", err)
			return
		}

		s.Notifier.NewString(r.Context(), &string)

		// successful
		s.Pages.HxRedirect(w, "/strings/"+user.Handle+"/"+string.Rkey)
	}
}

func (s *Strings) delete(w http.ResponseWriter, r *http.Request) {
	l := s.Logger.With("handler", "create")
	user := s.OAuth.GetUser(r)
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		s.Pages.Notice(w, "error", msg)
	}

	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		l.Error("malformed middleware")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	l = l.With("did", id.DID, "handle", id.Handle)

	rkey := chi.URLParam(r, "rkey")
	if rkey == "" {
		l.Error("malformed url, empty rkey")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if user.Did != id.DID.String() {
		fail("You cannot delete this string", fmt.Errorf("unauthorized deletion, %s != %s", user.Did, id.DID.String()))
		return
	}

	if err := db.DeleteString(
		s.Db,
		db.FilterEq("did", user.Did),
		db.FilterEq("rkey", rkey),
	); err != nil {
		fail("Failed to delete string.", err)
		return
	}

	s.Notifier.DeleteString(r.Context(), user.Did, rkey)

	s.Pages.HxRedirect(w, "/strings/"+user.Handle)
}

func (s *Strings) comment(w http.ResponseWriter, r *http.Request) {
}
