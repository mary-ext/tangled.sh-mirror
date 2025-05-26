package db

func (d *DB) SetOwner(did string) error {
	query := `insert into owner (id, did) values (?, ?)`
	_, err := d.db.Exec(query, 0, did)
	return err
}

func (d *DB) Owner() (string, error) {
	query := `select did from owner`

	var did string
	err := d.db.QueryRow(query).Scan(&did)
	if err != nil {
		return "", err
	}
	return did, nil
}
