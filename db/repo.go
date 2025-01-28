package db

func (d *DB) AddRepo(did string, name string, description string) error {
	_, err := d.db.Exec("insert into repos (did, name, description) values (?, ?, ?)", did, name, description)
	if err != nil {
		return err
	}
	return nil
}

func (d *DB) RemoveRepo(did string) error {
	_, err := d.db.Exec("delete from repos where did = ?", did)
	if err != nil {
		return err
	}
	return nil
}

func (d *DB) UpdateRepo(did string, name string, description string) error {
	_, err := d.db.Exec("update repos set name = ?, description = ? where did = ?", name, description, did)
	if err != nil {
		return err
	}
	return nil
}
