package cursor

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

type SqliteStore struct {
	db        *sql.DB
	tableName string
}

type SqliteStoreOpt func(*SqliteStore)

func WithTableName(name string) SqliteStoreOpt {
	return func(s *SqliteStore) {
		s.tableName = name
	}
}

func NewSQLiteStore(dbPath string, opts ...SqliteStoreOpt) (*SqliteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	store := &SqliteStore{
		db:        db,
		tableName: "cursors",
	}

	for _, o := range opts {
		o(store)
	}

	if err := store.init(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *SqliteStore) init() error {
	createTable := fmt.Sprintf(`
	create table if not exists %s (
		knot text primary key,
		cursor text
	);`, s.tableName)
	_, err := s.db.Exec(createTable)
	return err
}

func (s *SqliteStore) Set(knot string, cursor int64) {
	query := fmt.Sprintf(`
		insert into %s (knot, cursor)
		values (?, ?)
		on conflict(knot) do update set cursor=excluded.cursor;
	`, s.tableName)

	_, err := s.db.Exec(query, knot, cursor)

	if err != nil {
		// TODO: log here
	}
}

func (s *SqliteStore) Get(knot string) (cursor int64) {
	query := fmt.Sprintf(`
		select cursor from %s where knot = ?;
	`, s.tableName)
	err := s.db.QueryRow(query, knot).Scan(&cursor)

	if err != nil {
		if err != sql.ErrNoRows {
			// TODO: log here
		}
		return 0
	}

	return cursor
}
