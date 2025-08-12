package db

import (
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

func Make(dbPath string) (*DB, error) {
	// https://github.com/mattn/go-sqlite3#connection-string
	opts := []string{
		"_foreign_keys=1",
		"_journal_mode=WAL",
		"_synchronous=NORMAL",
		"_auto_vacuum=incremental",
	}

	db, err := sql.Open("sqlite3", dbPath+"?"+strings.Join(opts, "&"))
	if err != nil {
		return nil, err
	}

	// NOTE: If any other migration is added here, you MUST
	// copy the pattern in appview: use a single sql.Conn
	// for every migration.

	_, err = db.Exec(`
		create table if not exists _jetstream (
			id integer primary key autoincrement,
			last_time_us integer not null
		);

		create table if not exists known_dids (
			did text primary key
		);

		create table if not exists repos (
			id integer primary key autoincrement,
			knot text not null,
			owner text not null,
			name text not null,
			addedAt text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			unique(owner, name)
		);

		create table if not exists spindle_members (
			-- identifiers for the record
			id integer primary key autoincrement,
			did text not null,
			rkey text not null,

			-- data
			instance text not null,
			subject text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- constraints
			unique (did, instance, subject)
		);

		-- status event for a single workflow
		create table if not exists events (
			rkey text not null,
			nsid text not null,
			event text not null, -- json
			created integer not null -- unix nanos
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
