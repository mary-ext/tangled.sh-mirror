package xrpc

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"tangled.sh/tangled.sh/core/api/tangled"
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

	writeJson(w, response)
}
