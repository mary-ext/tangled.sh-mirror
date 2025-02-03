package state

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"
)

type SignerTransport struct {
	Secret string
}

func SignedClient(secret string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: SignerTransport{
			Secret: secret,
		},
	}
}

func (s SignerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	timestamp := time.Now().Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(s.Secret))
	message := req.Method + req.URL.Path + timestamp
	mac.Write([]byte(message))
	signature := hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("X-Signature", signature)
	req.Header.Set("X-Timestamp", timestamp)
	return http.DefaultTransport.RoundTrip(req)
}
