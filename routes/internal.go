package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/config"
	"github.com/icyphox/bild/db"
)

type InternalHandle struct {
	c  *config.Config
	db *db.DB
}

func SetupInternal(c *config.Config, db *db.DB) http.Handler {
	ih := &InternalHandle{
		c:  c,
		db: db,
	}

	r := chi.NewRouter()
	r.Route("/internal/allkeys", func(r chi.Router) {
		r.Get("/", ih.AllKeys)
	})

	return r
}

func (h *InternalHandle) returnJSON(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	res, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.Write(res)
	return err
}

func (h *InternalHandle) returnErr(w http.ResponseWriter, err error) error {
	w.WriteHeader(http.StatusInternalServerError)
	return h.returnJSON(w, map[string]string{
		"error": err.Error(),
	})
}

func (h *InternalHandle) AllKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.GetAllPublicKeys()
	if err != nil {
		h.returnErr(w, err)
		return
	}
	keyMap := map[string]string{}
	for _, key := range keys {
		keyMap[key.DID] = key.Key
	}
	if err := h.returnJSON(w, keyMap); err != nil {
		h.returnErr(w, err)
		return
	}
}
