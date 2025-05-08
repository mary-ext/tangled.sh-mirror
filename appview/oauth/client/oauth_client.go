package client

import (
	oauth "github.com/haileyok/atproto-oauth-golang"
	"github.com/haileyok/atproto-oauth-golang/helpers"
)

type OAuthClient struct {
	*oauth.Client
}

func NewClient(clientId, clientJwk, redirectUri string) (*OAuthClient, error) {
	k, err := helpers.ParseJWKFromBytes([]byte(clientJwk))
	if err != nil {
		return nil, err
	}

	cli, err := oauth.NewClient(oauth.ClientArgs{
		ClientId:    clientId,
		ClientJwk:   k,
		RedirectUri: redirectUri,
	})
	return &OAuthClient{cli}, err
}
