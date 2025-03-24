package db

func (d *DB) SaveLastTimeUs(lastTimeUs int64) error {
	_, err := d.db.Exec(`
		insert into _jetstream (id, last_time_us)
		values (1, ?)
		on conflict(id) do update set last_time_us = excluded.last_time_us
	`, lastTimeUs)
	return err
}

func (d *DB) GetLastTimeUs() (int64, error) {
	var lastTimeUs int64
	row := d.db.QueryRow(`select last_time_us from _jetstream where id = 1;`)
	err := row.Scan(&lastTimeUs)
	return lastTimeUs, err
}
