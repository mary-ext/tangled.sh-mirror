package models

import (
	"encoding/json"
	"time"
)

type PublicKey struct {
	Did     string `json:"did"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Rkey    string `json:"rkey"`
	Created *time.Time
}

func (p PublicKey) MarshalJSON() ([]byte, error) {
	type Alias PublicKey
	return json.Marshal(&struct {
		Created string `json:"created"`
		*Alias
	}{
		Created: p.Created.Format(time.RFC3339),
		Alias:   (*Alias)(&p),
	})
}
