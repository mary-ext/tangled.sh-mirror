package db

type DbWrapper struct {
	Execer
}

func (db DbWrapper) SaveLastTimeUs(lastTimeUs int64) error {
	_, err := db.Exec(`insert into _jetstream (last_time_us) values (?)`, lastTimeUs)
	return err
}

func (db DbWrapper) UpdateLastTimeUs(lastTimeUs int64) error {
	_, err := db.Exec(`update _jetstream set last_time_us = ? where rowid = 1`, lastTimeUs)
	if err != nil {
		return err
	}
	return nil
}

func (db DbWrapper) GetLastTimeUs() (int64, error) {
	var lastTimeUs int64
	row := db.QueryRow(`select last_time_us from _jetstream`)
	err := row.Scan(&lastTimeUs)
	return lastTimeUs, err
}
