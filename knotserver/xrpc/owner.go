package xrpc

import (
	"net/http"

	"tangled.sh/tangled.sh/core/api/tangled"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
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
