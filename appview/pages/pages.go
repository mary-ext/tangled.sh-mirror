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
