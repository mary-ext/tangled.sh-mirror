package pages

import (
	"embed"
	"html/template"
	"io"
	"sync"

	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
)

//go:embed *.html
var files embed.FS

var (
	cache = make(map[string]*template.Template)
	mutex sync.Mutex
)

func parse(file string) *template.Template {
	mutex.Lock()
	defer mutex.Unlock()

	if tmpl, found := cache[file]; found {
		return tmpl
	}

	tmpl := template.Must(
		template.New("layout.html").ParseFS(files, "layout.html", file),
	)

	cache[file] = tmpl
	return tmpl
}

type LoginParams struct {
}

func Login(w io.Writer, p LoginParams) error {
	return parse("login.html").Execute(w, p)
}

type TimelineParams struct {
	User *auth.User
}

func Timeline(w io.Writer, p TimelineParams) error {
	return parse("timeline.html").Execute(w, p)
}

type SettingsParams struct {
	User    *auth.User
	PubKeys []db.PublicKey
}

func Settings(w io.Writer, p SettingsParams) error {
	return parse("settings.html").Execute(w, p)
}

type KnotsParams struct {
	User          *auth.User
	Registrations []db.Registration
}

func Knots(w io.Writer, p KnotsParams) error {
	return parse("knots.html").Execute(w, p)
}

type KnotParams struct {
	User         *auth.User
	Registration *db.Registration
	Members      []string
	IsOwner      bool
}

func Knot(w io.Writer, p KnotParams) error {
	return parse("knot.html").Execute(w, p)
}

type NewRepoParams struct {
	User *auth.User
}

func NewRepo(w io.Writer, p NewRepoParams) error {
	return parse("new-repo.html").Execute(w, p)
}

type ProfilePageParams struct {
	LoggedInUser *auth.User
	UserDid      string
	UserHandle   string
	Repos        []db.Repo
}

func ProfilePage(w io.Writer, p ProfilePageParams) error {
	return parse("profile.html").Execute(w, p)
}
