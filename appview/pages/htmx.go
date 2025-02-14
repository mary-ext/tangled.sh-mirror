package pages

import (
	"fmt"
	"net/http"
)

// Notice performs a hx-oob-swap to replace the content of an element with a message.
// Pass the id of the element and the message to display.
func (s *Pages) Notice(w http.ResponseWriter, id, msg string) {
	html := fmt.Sprintf(`<span id="%s" hx-swap-oob="innerHTML">%s</span>`, id, msg)

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

// HxRedirect is a full page reload with a new location.
func (s *Pages) HxRedirect(w http.ResponseWriter, location string) {
	w.Header().Set("HX-Redirect", location)
	w.WriteHeader(http.StatusOK)
}

// HxLocation is an SPA-style navigation to a new location.
func (s *Pages) HxLocation(w http.ResponseWriter, location string) {
	w.Header().Set("HX-Location", location)
	w.WriteHeader(http.StatusOK)
}
