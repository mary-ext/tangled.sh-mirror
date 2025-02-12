package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

func Make(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		create table if not exists registrations (
			id integer primary key autoincrement,
			domain text not null unique,
			did text not null,
			secret text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			registered text
		);
		create table if not exists public_keys (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			key text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, name, key)
		);
		create table if not exists repos (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			knot text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, name, knot)
		);
		create table if not exists follows (
			user_did text not null,
			subject_did text not null,
			followed_at text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			primary key (user_did, subject_did),
			check (user_did <> subject_did)
		);
	`)
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}
