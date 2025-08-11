// an sqlite3 backed secret manager
package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SqliteManager struct {
	db        *sql.DB
	tableName string
}

type SqliteManagerOpt func(*SqliteManager)

func WithTableName(name string) SqliteManagerOpt {
	return func(s *SqliteManager) {
		s.tableName = name
	}
}

func NewSQLiteManager(dbPath string, opts ...SqliteManagerOpt) (*SqliteManager, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	manager := &SqliteManager{
		db:        db,
		tableName: "secrets",
	}

	for _, o := range opts {
		o(manager)
	}

	if err := manager.init(); err != nil {
		return nil, err
	}

	return manager, nil
}

// creates a table and sets up the schema, migrations if any can go here
func (s *SqliteManager) init() error {
	createTable :=
		`create table if not exists ` + s.tableName + `(
			id integer primary key autoincrement,
			repo text not null,
			key text not null,
			value text not null,
			created_at text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			created_by text not null,

			unique(repo, key)
		);`
	_, err := s.db.Exec(createTable)
	return err
}

func (s *SqliteManager) AddSecret(ctx context.Context, secret UnlockedSecret) error {
	query := fmt.Sprintf(`
		insert or ignore into %s (repo, key, value, created_by)
		values (?, ?, ?, ?);
	`, s.tableName)

	res, err := s.db.ExecContext(ctx, query, secret.Repo, secret.Key, secret.Value, secret.CreatedBy)
	if err != nil {
		return err
	}

	num, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if num == 0 {
		return ErrKeyAlreadyPresent
	}

	return nil
}

func (s *SqliteManager) RemoveSecret(ctx context.Context, secret Secret[any]) error {
	query := fmt.Sprintf(`
		delete from %s where repo = ? and key = ?;
	`, s.tableName)

	res, err := s.db.ExecContext(ctx, query, secret.Repo, secret.Key)
	if err != nil {
		return err
	}

	num, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if num == 0 {
		return ErrKeyNotFound
	}

	return nil
}

func (s *SqliteManager) GetSecretsLocked(ctx context.Context, didSlashRepo DidSlashRepo) ([]LockedSecret, error) {
	query := fmt.Sprintf(`
		select repo, key, created_at, created_by from %s where repo = ?;
	`, s.tableName)

	rows, err := s.db.QueryContext(ctx, query, didSlashRepo)
	if err != nil {
		return nil, err
	}

	var ls []LockedSecret
	for rows.Next() {
		var l LockedSecret
		var createdAt string
		if err = rows.Scan(&l.Repo, &l.Key, &createdAt, &l.CreatedBy); err != nil {
			return nil, err
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			l.CreatedAt = t
		}

		ls = append(ls, l)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return ls, nil
}

func (s *SqliteManager) GetSecretsUnlocked(ctx context.Context, didSlashRepo DidSlashRepo) ([]UnlockedSecret, error) {
	query := fmt.Sprintf(`
		select repo, key, value, created_at, created_by from %s where repo = ?;
	`, s.tableName)

	rows, err := s.db.QueryContext(ctx, query, didSlashRepo)
	if err != nil {
		return nil, err
	}

	var ls []UnlockedSecret
	for rows.Next() {
		var l UnlockedSecret
		var createdAt string
		if err = rows.Scan(&l.Repo, &l.Key, &l.Value, &createdAt, &l.CreatedBy); err != nil {
			return nil, err
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			l.CreatedAt = t
		}

		ls = append(ls, l)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return ls, nil
}
