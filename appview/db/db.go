package db

import (
	"context"
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

type Execer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
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
			rkey text not null,
			at_uri text not null unique,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, name, knot, rkey)
		);
		create table if not exists collaborators (
			id integer primary key autoincrement,
			did text not null,
			repo integer not null,
			foreign key (repo) references repos(id) on delete cascade
		);
		create table if not exists follows (
			user_did text not null,
			subject_did text not null,
			rkey text not null,
			followed_at text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			primary key (user_did, subject_did),
			check (user_did <> subject_did)
		);
		create table if not exists issues (
			id integer primary key autoincrement,
			owner_did text not null,
			repo_at text not null,
			issue_id integer not null,
			title text not null,
			body text not null,
			open integer not null default 1,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			issue_at text,
			unique(repo_at, issue_id),
			foreign key (repo_at) references repos(at_uri) on delete cascade
		);
		create table if not exists comments (
			id integer primary key autoincrement,
			owner_did text not null,
			issue_id integer not null,
			repo_at text not null,
			comment_id integer not null,
			comment_at text not null,
			body text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(issue_id, comment_id),
			foreign key (repo_at, issue_id) references issues(repo_at, issue_id) on delete cascade
		);
		create table if not exists _jetstream (
			id integer primary key autoincrement,
			last_time_us integer not null
		);

		create table if not exists repo_issue_seqs (
			repo_at text primary key,
			next_issue_id integer not null default 1
		);

		create table if not exists stars (
			id integer primary key autoincrement,
			starred_by_did text not null,
			repo_at text not null,
			rkey text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			foreign key (repo_at) references repos(at_uri) on delete cascade,
			unique(starred_by_did, repo_at)
		);

		create table if not exists migrations (
			id integer primary key autoincrement,
			name text unique
		)
	`)
	if err != nil {
		return nil, err
	}

	// run migrations
	runMigration(db, "add-description-to-repos", func(tx *sql.Tx) error {
		tx.Exec(`
			alter table repos add column description text check (length(description) <= 200);
		`)
		return nil
	})

	return &DB{db}, nil
}

type migrationFn = func(*sql.Tx) error

func runMigration(d *sql.DB, name string, migrationFn migrationFn) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	err = tx.QueryRow("select exists (select 1 from migrations where name = ?)", name).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		// run migration
		err = migrationFn(tx)
		if err != nil {
			log.Printf("Failed to run migration %s: %v", name, err)
			return err
		}

		// mark migration as complete
		_, err = tx.Exec("insert into migrations (name) values (?)", name)
		if err != nil {
			log.Printf("Failed to mark migration %s as complete: %v", name, err)
			return err
		}

		// commit the transaction
		if err := tx.Commit(); err != nil {
			return err
		}

		log.Printf("migration %s applied successfully", name)
	} else {
		log.Printf("skipped migration %s, already applied", name)
	}

	return nil
}
