package db

func (d *DB) AddUser(did string) error {
	_, err := d.db.Exec(`insert into users (did) values (?)`, did)
	return err
}

func (d *DB) RemoveUser(did string) error {
	_, err := d.db.Exec(`delete from users where did = ?`, did)
	return err
}
