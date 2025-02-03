package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

func Setup(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		create table if not exists known_dids (
			did text primary key
		);

		create table if not exists public_keys (
			id integer primary key autoincrement,
			did text not null,
			key text not null,
			created timestamp default current_timestamp,
			unique(did, key),
			foreign key (did) references known_dids(did) on delete cascade
		);

		create table if not exists repos (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			description text not null,
			created timestamp default current_timestamp,
			unique(did, name)
		);

		create table if not exists _jetstream (
			id integer primary key autoincrement,
			last_time_us integer not null
		);
	`)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}
