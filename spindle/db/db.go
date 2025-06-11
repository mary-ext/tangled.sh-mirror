package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

func Make(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		pragma journal_mode = WAL;
		pragma synchronous = normal;
		pragma foreign_keys = on;
		pragma temp_store = memory;
		pragma mmap_size = 30000000000;
		pragma page_size = 32768;
		pragma auto_vacuum = incremental;
		pragma busy_timeout = 5000;

		create table if not exists known_dids (
			did text primary key
		);

		create table if not exists pipeline_status (
			rkey text not null,
			pipeline text not null,
			status text not null,

			-- only set if status is 'failed'
			error text,
			exit_code integer,

			started_at timestamp not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			updated_at timestamp not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			finished_at timestamp,

			primary key (rkey)
		);
	`)
	if err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

func (d *DB) SaveLastTimeUs(lastTimeUs int64) error {
	_, err := d.Exec(`
		insert into _jetstream (id, last_time_us)
		values (1, ?)
		on conflict(id) do update set last_time_us = excluded.last_time_us
	`, lastTimeUs)
	return err
}

func (d *DB) GetLastTimeUs() (int64, error) {
	var lastTimeUs int64
	row := d.QueryRow(`select last_time_us from _jetstream where id = 1;`)
	err := row.Scan(&lastTimeUs)
	return lastTimeUs, err
}
