package xrpc

import (
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

	writeJson(w, response)
}
