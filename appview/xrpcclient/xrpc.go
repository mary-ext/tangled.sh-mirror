package xrpcclient

import (
	"errors"
	"net/http"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
)

var (
	ErrXrpcUnsupported  = errors.New("xrpc not supported on this knot")
	ErrXrpcUnauthorized = errors.New("unauthorized xrpc request")
	ErrXrpcFailed       = errors.New("xrpc request failed")
	ErrXrpcInvalid      = errors.New("invalid xrpc request")
)

// produces a more manageable error
func HandleXrpcErr(err error) error {
	if err == nil {
		return nil
	}

	var xrpcerr *indigoxrpc.Error
	if ok := errors.As(err, &xrpcerr); !ok {
		return ErrXrpcInvalid
	}

	switch xrpcerr.StatusCode {
	case http.StatusNotFound:
		return ErrXrpcUnsupported
	case http.StatusUnauthorized:
		return ErrXrpcUnauthorized
	default:
		return ErrXrpcFailed
	}
}
