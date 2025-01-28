package routes

import (
	"fmt"
	"log"
	"net/http"
)

func (h *Handle) Write404(w http.ResponseWriter) {
	w.WriteHeader(404)
	if err := h.t.ExecuteTemplate(w, "errors/404", nil); err != nil {
		log.Printf("404 template: %s", err)
	}
}

func (h *Handle) Write500(w http.ResponseWriter) {
	w.WriteHeader(500)
	if err := h.t.ExecuteTemplate(w, "errors/500", nil); err != nil {
		log.Printf("500 template: %s", err)
	}
}

func (h *Handle) WriteOOBNotice(w http.ResponseWriter, id, msg string) {
	html := fmt.Sprintf(`<span id="%s" hx-swap-oob="innerHTML">%s</span>`, id, msg)

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}
