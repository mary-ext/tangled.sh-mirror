package xrpc

import (
	"encoding/json"
	"net/http"

	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) Owner(w http.ResponseWriter, r *http.Request) {
	owner := x.Config.Server.Owner
	if owner == "" {
		writeError(w, xrpcerr.OwnerNotFoundError, http.StatusInternalServerError)
		return
	}

	response := tangled.Owner_Output{
		Owner: owner,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		x.Logger.Error("failed to encode response", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to encode response"),
		), http.StatusInternalServerError)
		return
	}
}
