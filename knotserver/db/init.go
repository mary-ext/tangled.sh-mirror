package db

import (
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

func Setup(dbPath string) (*DB, error) {
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
		create table if not exists known_dids (
			did text primary key
		);

		create table if not exists public_keys (
			id integer primary key autoincrement,
			did text not null,
			key text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, key),
			foreign key (did) references known_dids(did) on delete cascade
		);

		create table if not exists _jetstream (
			id integer primary key autoincrement,
			last_time_us integer not null
		);

		create table if not exists events (
			rkey text not null,
			nsid text not null,
			event text not null, -- json
			created integer not null default (strftime('%s', 'now')),
			primary key (rkey, nsid)
		);
	`)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}
