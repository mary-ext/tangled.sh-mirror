package db

import (
	"encoding/json"
	"time"
)

func (d *DB) AddPublicKey(did, name, key string) error {
	query := `insert or ignore into public_keys (did, name, key) values (?, ?, ?)`
	_, err := d.db.Exec(query, did, name, key)
	return err
}

func (d *DB) RemovePublicKey(did string) error {
	query := `delete from public_keys where did = ?`
	_, err := d.db.Exec(query, did)
	return err
}

type PublicKey struct {
	Did     string `json:"did"`
	Key     string `json:"key"`
	Name    string `json:"name"`
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

func (d *DB) GetAllPublicKeys() ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := d.db.Query(`select key, name, did, created from public_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		var createdAt string
		if err := rows.Scan(&publicKey.Key, &publicKey.Name, &publicKey.Did, &createdAt); err != nil {
			return nil, err
		}
		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
		publicKey.Created = &createdAtTime
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func (d *DB) GetPublicKeys(did string) ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := d.db.Query(`select did, key, name, created from public_keys where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		var createdAt string
		if err := rows.Scan(&publicKey.Did, &publicKey.Key, &publicKey.Name, &createdAt); err != nil {
			return nil, err
		}
		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
		publicKey.Created = &createdAtTime
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}
