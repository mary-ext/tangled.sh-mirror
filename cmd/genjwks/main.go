// adapted from https://github.com/haileyok/atproto-oauth-golang

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
)

func main() {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}

	key, err := jwk.FromRaw(privKey)
	if err != nil {
		panic(err)
	}

	kid := fmt.Sprintf("%d", time.Now().Unix())

	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		panic(err)
	}

	b, err := json.Marshal(key)
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile("./jwks.json", b, 0644); err != nil {
		panic(err)
	}
}
