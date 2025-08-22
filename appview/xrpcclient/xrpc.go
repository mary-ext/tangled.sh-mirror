package xrpcclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	oauth "tangled.sh/icyphox.sh/atproto-oauth"
)

type Client struct {
	*oauth.XrpcClient
	authArgs *oauth.XrpcAuthedRequestArgs
}

func NewClient(client *oauth.XrpcClient, authArgs *oauth.XrpcAuthedRequestArgs) *Client {
	return &Client{
		XrpcClient: client,
		authArgs:   authArgs,
	}
}

func (c *Client) RepoPutRecord(ctx context.Context, input *atproto.RepoPutRecord_Input) (*atproto.RepoPutRecord_Output, error) {
	var out atproto.RepoPutRecord_Output
	if err := c.Do(ctx, c.authArgs, xrpc.Procedure, "application/json", "com.atproto.repo.putRecord", nil, input, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

func (c *Client) RepoApplyWrites(ctx context.Context, input *atproto.RepoApplyWrites_Input) (*atproto.RepoApplyWrites_Output, error) {
	var out atproto.RepoApplyWrites_Output
	if err := c.Do(ctx, c.authArgs, xrpc.Procedure, "application/json", "com.atproto.repo.applyWrites", nil, input, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

func (c *Client) RepoGetRecord(ctx context.Context, cid string, collection string, repo string, rkey string) (*atproto.RepoGetRecord_Output, error) {
	var out atproto.RepoGetRecord_Output

	params := map[string]interface{}{
		"cid":        cid,
		"collection": collection,
		"repo":       repo,
		"rkey":       rkey,
	}
	if err := c.Do(ctx, c.authArgs, xrpc.Query, "", "com.atproto.repo.getRecord", params, nil, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

func (c *Client) RepoUploadBlob(ctx context.Context, input io.Reader) (*atproto.RepoUploadBlob_Output, error) {
	var out atproto.RepoUploadBlob_Output
	if err := c.Do(ctx, c.authArgs, xrpc.Procedure, "*/*", "com.atproto.repo.uploadBlob", nil, input, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

func (c *Client) SyncGetBlob(ctx context.Context, cid string, did string) ([]byte, error) {
	buf := new(bytes.Buffer)

	params := map[string]interface{}{
		"cid": cid,
		"did": did,
	}
	if err := c.Do(ctx, c.authArgs, xrpc.Query, "", "com.atproto.sync.getBlob", params, nil, buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (c *Client) RepoDeleteRecord(ctx context.Context, input *atproto.RepoDeleteRecord_Input) (*atproto.RepoDeleteRecord_Output, error) {
	var out atproto.RepoDeleteRecord_Output
	if err := c.Do(ctx, c.authArgs, xrpc.Procedure, "application/json", "com.atproto.repo.deleteRecord", nil, input, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

func (c *Client) ServerGetServiceAuth(ctx context.Context, aud string, exp int64, lxm string) (*atproto.ServerGetServiceAuth_Output, error) {
	var out atproto.ServerGetServiceAuth_Output

	params := map[string]interface{}{
		"aud": aud,
		"exp": exp,
		"lxm": lxm,
	}
	if err := c.Do(ctx, c.authArgs, xrpc.Query, "", "com.atproto.server.getServiceAuth", params, nil, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// produces a more manageable error
func HandleXrpcErr(err error) error {
	if err == nil {
		return nil
	}

	var xrpcerr *indigoxrpc.Error
	if ok := errors.As(err, &xrpcerr); !ok {
		return fmt.Errorf("Recieved invalid XRPC error response.")
	}

	switch xrpcerr.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("XRPC is unsupported on this knot, consider upgrading your knot.")
	case http.StatusUnauthorized:
		return fmt.Errorf("Unauthorized XRPC request.")
	default:
		return fmt.Errorf("Failed to perform operation. Try again later.")
	}
}
