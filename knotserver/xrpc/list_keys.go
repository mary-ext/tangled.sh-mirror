package xrpc

import (
	"encoding/json"
	"net/http"
	"strconv"

	"tangled.sh/tangled.sh/core/api/tangled"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
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
