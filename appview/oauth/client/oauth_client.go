package client

import (
	oauth "tangled.org/anirudh.fi/atproto-oauth"
	"tangled.org/anirudh.fi/atproto-oauth/helpers"
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
