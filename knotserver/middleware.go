package knotserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

func (h *Handle) VerifySignature(next http.Handler) http.Handler {
	if h.c.Server.Dev {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature := r.Header.Get("X-Signature")
		log.Println(signature)
		if signature == "" || !h.verifyHMAC(signature, r) {
			writeError(w, "signature verification failed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handle) verifyHMAC(signature string, r *http.Request) bool {
	secret := h.c.Server.Secret
	timestamp := r.Header.Get("X-Timestamp")
	if timestamp == "" {
		return false
	}

	// Verify that the timestamp is not older than a minute
	reqTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false
	}
	if time.Since(reqTime) > time.Minute {
		return false
	}

	message := r.Method + r.URL.Path + timestamp

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	expectedMAC := mac.Sum(nil)

	signatureBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	return hmac.Equal(signatureBytes, expectedMAC)
}
