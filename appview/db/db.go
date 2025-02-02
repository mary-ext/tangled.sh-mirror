package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

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
			created timestamp default current_timestamp,
			registered timestamp);
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

type Registration struct {
	Domain     string
	ByDid      string
	Created    *time.Time
	Registered *time.Time
}

func (r *Registration) Status() Status {
	if r.Registered != nil {
		return Registered
	} else {
		return Pending
	}
}

type Status uint32

const (
	Registered Status = iota
	Pending
)

// returns registered status, did of owner, error
func (d *DB) RegistrationsByDid(did string) ([]Registration, error) {
	var registrations []Registration

	rows, err := d.db.Query(`
		select domain, did, created, registered from registrations
		where did = ?
	`, did)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var createdAt *int64
		var registeredAt *int64
		var registration Registration
		err = rows.Scan(&registration.Domain, &registration.ByDid, &createdAt, &registeredAt)

		if err != nil {
			log.Println(err)
		} else {
			createdAtTime := time.Unix(*createdAt, 0)

			var registeredAtTime *time.Time
			if registeredAt != nil {
				x := time.Unix(*registeredAt, 0)
				registeredAtTime = &x
			}

			registration.Created = &createdAtTime
			registration.Registered = registeredAtTime
			registrations = append(registrations, registration)
		}
	}

	return registrations, nil
}

// returns registered status, did of owner, error
func (d *DB) RegistrationByDomain(domain string) (*Registration, error) {
	var createdAt *int64
	var registeredAt *int64
	var registration Registration
	err := d.db.QueryRow(`
		select domain, did, created, registered from registrations
		where domain = ?
	`, domain).Scan(&registration.Domain, &registration.ByDid, &createdAt, &registeredAt)

	createdAtTime := time.Unix(*createdAt, 0)
	var registeredAtTime *time.Time
	if registeredAt != nil {
		x := time.Unix(*registeredAt, 0)
		registeredAtTime = &x
	}

	registration.Created = &createdAtTime
	registration.Registered = registeredAtTime

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		} else {
			return nil, err
		}
	}

	return &registration, nil
}

func (d *DB) GenerateRegistrationKey(domain, did string) (string, error) {
	// sanity check: does this domain already have a registration?
	reg, err := d.RegistrationByDomain(domain)
	if err != nil {
		return "", err
	}

	// registration is open
	if reg != nil {
		switch reg.Status() {
		case Registered:
			// already registered by `owner`
			return "", fmt.Errorf("%s already registered by %s", domain, reg.ByDid)
		case Pending:
			// TODO: be loud about this
			log.Printf("%s registered by %s, status pending", domain, reg.ByDid)
		}
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
