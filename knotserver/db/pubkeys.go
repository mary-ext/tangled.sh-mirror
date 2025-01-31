package db

import "time"

func (d *DB) AddPublicKey(did, name, key string) error {
	query := `insert into public_keys (did, name, key, created) values (?, ?, ?, ?)`
	_, err := d.db.Exec(query, did, name, key, time.Now())
	return err
}

func (d *DB) RemovePublicKey(did string) error {
	query := `delete from public_keys where did = ?`
	_, err := d.db.Exec(query, did)
	return err
}

type PublicKey struct {
	Key     string
	Name    string
	DID     string
	Created time.Time
}

func (pk *PublicKey) JSON() map[string]interface{} {
	return map[string]interface{}{
		pk.DID: map[string]interface{}{
			"key":     pk.Key,
			"name":    pk.Name,
			"created": pk.Created,
		},
	}
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
		if err := rows.Scan(&publicKey.Key, &publicKey.Name, &publicKey.DID, &publicKey.Created); err != nil {
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

	rows, err := d.db.Query(`select did, key, name, created from public_keys where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		if err := rows.Scan(&publicKey.DID, &publicKey.Key, &publicKey.Name, &publicKey.Created); err != nil {
			return nil, err
		}
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}
