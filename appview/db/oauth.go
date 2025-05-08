package db

type OAuthRequest struct {
	ID                  uint
	AuthserverIss       string
	Handle              string
	State               string
	Did                 string
	PdsUrl              string
	PkceVerifier        string
	DpopAuthserverNonce string
	DpopPrivateJwk      string
}

func SaveOAuthRequest(e Execer, oauthRequest OAuthRequest) error {
	_, err := e.Exec(`
		insert into oauth_requests (
			auth_server_iss,
			state,
			handle,
			did,
			pds_url,
			pkce_verifier,
			dpop_auth_server_nonce,
			dpop_private_jwk
		) values (?, ?, ?, ?, ?, ?, ?, ?)`,
		oauthRequest.AuthserverIss,
		oauthRequest.State,
		oauthRequest.Handle,
		oauthRequest.Did,
		oauthRequest.PdsUrl,
		oauthRequest.PkceVerifier,
		oauthRequest.DpopAuthserverNonce,
		oauthRequest.DpopPrivateJwk,
	)
	return err
}

func GetOAuthRequestByState(e Execer, state string) (OAuthRequest, error) {
	var req OAuthRequest
	err := e.QueryRow(`
		select
			id,
			auth_server_iss,
			handle,
			state,
			did,
			pds_url,
			pkce_verifier,
			dpop_auth_server_nonce,
			dpop_private_jwk
		from oauth_requests
		where state = ?`, state).Scan(
		&req.ID,
		&req.AuthserverIss,
		&req.Handle,
		&req.State,
		&req.Did,
		&req.PdsUrl,
		&req.PkceVerifier,
		&req.DpopAuthserverNonce,
		&req.DpopPrivateJwk,
	)
	return req, err
}

func DeleteOAuthRequestByState(e Execer, state string) error {
	_, err := e.Exec(`
		delete from oauth_requests
		where state = ?`, state)
	return err
}

type OAuthSession struct {
	ID                  uint
	Handle              string
	Did                 string
	PdsUrl              string
	AccessJwt           string
	RefreshJwt          string
	AuthServerIss       string
	DpopPdsNonce        string
	DpopAuthserverNonce string
	DpopPrivateJwk      string
	Expiry              string
}

func SaveOAuthSession(e Execer, session OAuthSession) error {
	_, err := e.Exec(`
  insert into oauth_sessions (
   did,
   handle,
   pds_url,
   access_jwt,
   refresh_jwt,
   auth_server_iss,
   dpop_auth_server_nonce,
   dpop_private_jwk,
   expiry
  ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.Did,
		session.Handle,
		session.PdsUrl,
		session.AccessJwt,
		session.RefreshJwt,
		session.AuthServerIss,
		session.DpopAuthserverNonce,
		session.DpopPrivateJwk,
		session.Expiry,
	)
	return err
}

func RefreshOAuthSession(e Execer, did string, accessJwt, refreshJwt, expiry string) error {
	_, err := e.Exec(`
  update oauth_sessions
  set access_jwt = ?, refresh_jwt = ?, expiry = ?
  where did = ?`,
		accessJwt,
		refreshJwt,
		expiry,
		did,
	)
	return err
}

func GetOAuthSessionByDid(e Execer, did string) (*OAuthSession, error) {
	var session OAuthSession
	err := e.QueryRow(`
  select
   id,
   did,
   handle,
   pds_url,
   access_jwt,
   refresh_jwt,
   auth_server_iss,
   dpop_auth_server_nonce,
   dpop_private_jwk,
   expiry
  from oauth_sessions
  where did = ?`, did).Scan(
		&session.ID,
		&session.Did,
		&session.Handle,
		&session.PdsUrl,
		&session.AccessJwt,
		&session.RefreshJwt,
		&session.AuthServerIss,
		&session.DpopAuthserverNonce,
		&session.DpopPrivateJwk,
		&session.Expiry,
	)
	return &session, err
}

func DeleteOAuthSessionByDid(e Execer, did string) error {
	_, err := e.Exec(`
  delete from oauth_sessions
  where did = ?`, did)
	return err
}

func UpdateDpopPdsNonce(e Execer, did string, dpopPdsNonce string) error {
	_, err := e.Exec(`
  update oauth_sessions
  set dpop_pds_nonce = ?
  where did = ?`,
		dpopPdsNonce,
		did,
	)
	return err
}
