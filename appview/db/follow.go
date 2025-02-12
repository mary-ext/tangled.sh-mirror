package db

func (d *DB) AddFollow(userDid, subjectDid string) error {
	query := `insert into follows (user_did, subject_did) values (?, ?)`
	_, err := d.db.Exec(query, userDid, subjectDid)
	return err
}
