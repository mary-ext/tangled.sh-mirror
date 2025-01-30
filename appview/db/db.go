package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
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
			created integer default (strftime('%s', 'now')),
			registered integer
		);
	`)
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

type RegStatus uint32

const (
	Registered RegStatus = iota
	Unregistered
	Pending
)

// returns registered status, did of owner, error
func (d *DB) RegistrationStatus(domain string) (RegStatus, string, error) {
	var registeredBy string
	var registratedAt *uint64
	err := d.db.QueryRow(`
		select did, registered from registrations
		where domain = ?
	`, domain).Scan(&registeredBy, &registratedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return Unregistered, "", nil
		} else {
			return Unregistered, "", err
		}
	}

	if registratedAt != nil {
		return Registered, registeredBy, nil
	} else {
		return Pending, registeredBy, nil
	}
}

func (d *DB) GenerateRegistrationKey(domain, did string) (string, error) {
	// sanity check: does this domain already have a registration?
	status, owner, err := d.RegistrationStatus(domain)
	if err != nil {
		return "", err
	}
	switch status {
	case Registered:
		// already registered by `owner`
		return "", fmt.Errorf("%s already registered by %s", domain, owner)
	case Pending:
		log.Printf("%s registered by %s, status pending", domain, owner)
		// TODO: provide a warning here, and allow the current user to overwrite
		// the registration, this prevents users from registering domains that they
		// do not own
	default:
		// ok, we can register this domain
	}

	secret := uuid.New().String()
	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(secret), 3)

	if err != nil {
		return "", err
	}

	_, err = d.db.Exec(`
		insert into registrations (domain, did, secret)
		values (?, ?, ?)
		on conflict(domain) do update set did = excluded.did, secret = excluded.secret
		`, domain, did, fmt.Sprintf("%x", hashedSecret))

	if err != nil {
		return "", err
	}

	return secret, nil
}

func (d *DB) Register(domain, secret string) error {
	ctx := context.TODO()

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	res := tx.QueryRow(`select secret from registrations where domain = ?`, domain)

	var storedSecret string
	err = res.Scan(&storedSecret)
	if err != nil {
		return err
	}

	err = bcrypt.CompareHashAndPassword([]byte(storedSecret), []byte(secret))
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		update registrations
		set registered = strftime('%s', 'now')
		where domain = ?;
		`, domain)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}
