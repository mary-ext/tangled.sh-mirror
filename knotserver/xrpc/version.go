package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"

	"tangled.sh/tangled.sh/core/api/tangled"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

// version is set during build time.
var version string

func (x *Xrpc) Version(w http.ResponseWriter, r *http.Request) {
	if version == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			http.Error(w, "failed to read build info", http.StatusInternalServerError)
			return
		}

		var modVer string
		var sha string
		var modified bool

		for _, mod := range info.Deps {
			if mod.Path == "tangled.sh/tangled.sh/knotserver/xrpc" {
				modVer = mod.Version
				break
			}
		}

		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				sha = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}

		if modVer == "" {
			modVer = "unknown"
		}

		if sha == "" {
			version = modVer
		} else if modified {
			version = fmt.Sprintf("%s (%s with modifications)", modVer, sha)
		} else {
			version = fmt.Sprintf("%s (%s)", modVer, sha)
		}
	}

	response := tangled.KnotVersion_Output{
		Version: version,
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
