package db

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

func (d *DB) GetPublicKey(did string) (string, error) {
	var key string
	query := `select key from public_keys where did = ?`
	err := d.db.QueryRow(query, did).Scan(&key)
	if err != nil {
		return "", err
	}
	return key, nil
}
