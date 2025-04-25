package db

import (
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
)

type PublicKey struct {
	Did string
	tangled.PublicKey
}

func (d *DB) AddPublicKeyFromRecord(did string, recordIface map[string]interface{}) error {
	record := make(map[string]string)
	for k, v := range recordIface {
		if str, ok := v.(string); ok {
			record[k] = str
		}
	}

	pk := PublicKey{
		Did: did,
	}
	pk.Key = record["key"]
	pk.CreatedAt = record["createdAt"]

	return d.AddPublicKey(pk)
}

func (d *DB) AddPublicKey(pk PublicKey) error {
	if pk.CreatedAt == "" {
		pk.CreatedAt = time.Now().Format(time.RFC3339)
	}

	query := `insert or ignore into public_keys (did, key, created) values (?, ?, ?)`
	_, err := d.db.Exec(query, pk.Did, pk.Key, pk.CreatedAt)
	return err
}

func (d *DB) RemovePublicKey(did string) error {
	query := `delete from public_keys where did = ?`
	_, err := d.db.Exec(query, did)
	return err
}

func (pk *PublicKey) JSON() map[string]any {
	return map[string]any{
		"did":       pk.Did,
		"key":       pk.Key,
		"createdAt": pk.CreatedAt,
	}
}

func (d *DB) GetAllPublicKeys() ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := d.db.Query(`select key, did, created from public_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		if err := rows.Scan(&publicKey.Key, &publicKey.Did, &publicKey.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func (d *DB) GetPublicKeys(did string) ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := d.db.Query(`select did, key, created from public_keys where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		if err := rows.Scan(&publicKey.Did, &publicKey.Key, &publicKey.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}
