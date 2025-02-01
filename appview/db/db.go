package db

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/google/uuid"

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
			created integer default (strftime('%s', 'now')),
			registered integer);
		create table if not exists public_keys (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			key text not null,
			created timestamp default current_timestamp,
			unique(did, name, key)
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

	_, err = d.db.Exec(`
		insert into registrations (domain, did, secret)
		values (?, ?, ?)
		on conflict(domain) do update set did = excluded.did, secret = excluded.secret
		`, domain, did, secret)

	if err != nil {
		return "", err
	}

	return secret, nil
}

func (d *DB) GetRegistrationKey(domain string) (string, error) {
	res := d.db.QueryRow(`select secret from registrations where domain = ?`, domain)

	var secret string
	err := res.Scan(&secret)
	if err != nil || secret == "" {
		return "", err
	}

	log.Println("domain, secret: ", domain, secret)

	return secret, nil
}

func (d *DB) Register(domain string) error {
	_, err := d.db.Exec(`
		update registrations
		set registered = strftime('%s', 'now')
		where domain = ?;
		`, domain)

	if err != nil {
		return err
	}

	return nil
}

// type Registration struct {
// 	status RegStatus
// }
// func (d *DB) RegistrationsForDid(did string) ()
