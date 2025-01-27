package db

import "time"

func (d *DB) AddPublicKey(did, name, key string) error {
	query := `insert into public_keys (did, name, key) values (?, ?, ?)`
	_, err := d.db.Exec(query, did, name, key)
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
	Created time.Time
}

func (d *DB) GetPublicKeys(did string) ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := d.db.Query(`select key, name, created from public_keys where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey PublicKey
		if err := rows.Scan(&publicKey.Key, &publicKey.Name, &publicKey.Created); err != nil {
			return nil, err
		}
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}
