package xrpc

import (
	"net/http"
	"strconv"

	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) ListKeys(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("cursor")

	limit := 100 // default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	keys, nextCursor, err := x.Db.GetPublicKeysPaginated(limit, cursor)
	if err != nil {
		x.Logger.Error("failed to get public keys", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to retrieve public keys"),
		), http.StatusInternalServerError)
		return
	}

	publicKeys := make([]*tangled.KnotListKeys_PublicKey, 0, len(keys))
	for _, key := range keys {
		publicKeys = append(publicKeys, &tangled.KnotListKeys_PublicKey{
			Did:       key.Did,
			Key:       key.Key,
			CreatedAt: key.CreatedAt,
		})
	}

	response := tangled.KnotListKeys_Output{
		Keys: publicKeys,
	}

	if nextCursor != "" {
		response.Cursor = &nextCursor
	}

	writeJson(w, response)
}
