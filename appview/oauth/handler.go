package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/posthog/posthog-go"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/consts"
	"tangled.org/core/tid"
)

func (o *OAuth) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/oauth/client-metadata.json", o.clientMetadata)
	r.Get("/oauth/jwks.json", o.jwks)
	r.Get("/oauth/callback", o.callback)
	return r
}

func (o *OAuth) clientMetadata(w http.ResponseWriter, r *http.Request) {
	doc := o.ClientApp.Config.ClientMetadata()
	doc.JWKSURI = &o.JwksUri

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (o *OAuth) jwks(w http.ResponseWriter, r *http.Request) {
	jwks := o.Config.OAuth.Jwks
	pubKey, err := pubKeyFromJwk(jwks)
	if err != nil {
		o.Logger.Error("error parsing public key", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]any{
		"keys": []jwk.Key{pubKey},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (o *OAuth) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessData, err := o.ClientApp.ProcessCallback(ctx, r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := o.SaveSession(w, r, sessData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	o.Logger.Debug("session saved successfully")
	go o.addToDefaultKnot(sessData.AccountDID.String())
	go o.addToDefaultSpindle(sessData.AccountDID.String())

	if !o.Config.Core.Dev {
		err = o.Posthog.Enqueue(posthog.Capture{
			DistinctId: sessData.AccountDID.String(),
			Event:      "signin",
		})
		if err != nil {
			o.Logger.Error("failed to enqueue posthog event", "err", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func (o *OAuth) addToDefaultSpindle(did string) {
	l := o.Logger.With("subject", did)

	// use the tangled.sh app password to get an accessJwt
	// and create an sh.tangled.spindle.member record with that
	spindleMembers, err := db.GetSpindleMembers(
		o.Db,
		db.FilterEq("instance", "spindle.tangled.sh"),
		db.FilterEq("subject", did),
	)
	if err != nil {
		l.Error("failed to get spindle members", "err", err)
		return
	}

	if len(spindleMembers) != 0 {
		l.Warn("already a member of the default spindle")
		return
	}

	l.Debug("adding to default spindle")
	session, err := o.createAppPasswordSession(o.Config.Core.AppPassword, consts.TangledDid)
	if err != nil {
		l.Error("failed to create session", "err", err)
		return
	}

	record := tangled.SpindleMember{
		LexiconTypeID: "sh.tangled.spindle.member",
		Subject:       did,
		Instance:      consts.DefaultSpindle,
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	if err := session.putRecord(record, tangled.SpindleMemberNSID); err != nil {
		l.Error("failed to add to default spindle", "err", err)
		return
	}

	l.Debug("successfully added to default spindle", "did", did)
}

func (o *OAuth) addToDefaultKnot(did string) {
	l := o.Logger.With("subject", did)

	// use the tangled.sh app password to get an accessJwt
	// and create an sh.tangled.spindle.member record with that

	allKnots, err := o.Enforcer.GetKnotsForUser(did)
	if err != nil {
		l.Error("failed to get knot members for did", "err", err)
		return
	}

	if slices.Contains(allKnots, consts.DefaultKnot) {
		l.Warn("already a member of the default knot")
		return
	}

	l.Debug("addings to default knot")
	session, err := o.createAppPasswordSession(o.Config.Core.TmpAltAppPassword, consts.IcyDid)
	if err != nil {
		l.Error("failed to create session", "err", err)
		return
	}

	record := tangled.KnotMember{
		LexiconTypeID: "sh.tangled.knot.member",
		Subject:       did,
		Domain:        consts.DefaultKnot,
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	if err := session.putRecord(record, tangled.KnotMemberNSID); err != nil {
		l.Error("failed to add to default knot", "err", err)
		return
	}

	if err := o.Enforcer.AddKnotMember(consts.DefaultKnot, did); err != nil {
		l.Error("failed to set up enforcer rules", "err", err)
		return
	}

	l.Debug("successfully addeds to default Knot")
}

// create a session using apppasswords
type session struct {
	AccessJwt   string `json:"accessJwt"`
	PdsEndpoint string
	Did         string
}

func (o *OAuth) createAppPasswordSession(appPassword, did string) (*session, error) {
	if appPassword == "" {
		return nil, fmt.Errorf("no app password configured, skipping member addition")
	}

	resolved, err := o.IdResolver.ResolveIdent(context.Background(), did)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tangled.sh DID %s: %v", did, err)
	}

	pdsEndpoint := resolved.PDSEndpoint()
	if pdsEndpoint == "" {
		return nil, fmt.Errorf("no PDS endpoint found for tangled.sh DID %s", did)
	}

	sessionPayload := map[string]string{
		"identifier": did,
		"password":   appPassword,
	}
	sessionBytes, err := json.Marshal(sessionPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session payload: %v", err)
	}

	sessionURL := pdsEndpoint + "/xrpc/com.atproto.server.createSession"
	sessionReq, err := http.NewRequestWithContext(context.Background(), "POST", sessionURL, bytes.NewBuffer(sessionBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create session request: %v", err)
	}
	sessionReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	sessionResp, err := client.Do(sessionReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %v", err)
	}
	defer sessionResp.Body.Close()

	if sessionResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to create session: HTTP %d", sessionResp.StatusCode)
	}

	var session session
	if err := json.NewDecoder(sessionResp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode session response: %v", err)
	}

	session.PdsEndpoint = pdsEndpoint
	session.Did = did

	return &session, nil
}

func (s *session) putRecord(record any, collection string) error {
	recordBytes, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal knot member record: %w", err)
	}

	payload := map[string]any{
		"repo":       s.Did,
		"collection": collection,
		"rkey":       tid.TID(),
		"record":     json.RawMessage(recordBytes),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request payload: %w", err)
	}

	url := s.PdsEndpoint + "/xrpc/com.atproto.repo.putRecord"
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.AccessJwt)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to add user to default service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to add user to default service: HTTP %d", resp.StatusCode)
	}

	return nil
}
