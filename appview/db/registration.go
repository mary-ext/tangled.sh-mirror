package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"
)

// Registration represents a knot registration. Knot would've been a better
// name but we're stuck with this for historical reasons.
type Registration struct {
	Id         int64
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
func RegistrationsByDid(e Execer, did string) ([]Registration, error) {
	var registrations []Registration

	rows, err := e.Query(`
		select id, domain, did, created, registered from registrations
		where did = ?
	`, did)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var createdAt *string
		var registeredAt *string
		var registration Registration
		err = rows.Scan(&registration.Id, &registration.Domain, &registration.ByDid, &createdAt, &registeredAt)

		if err != nil {
			log.Println(err)
		} else {
			createdAtTime, _ := time.Parse(time.RFC3339, *createdAt)
			var registeredAtTime *time.Time
			if registeredAt != nil {
				x, _ := time.Parse(time.RFC3339, *registeredAt)
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
func RegistrationByDomain(e Execer, domain string) (*Registration, error) {
	var createdAt *string
	var registeredAt *string
	var registration Registration

	err := e.QueryRow(`
		select id, domain, did, created, registered from registrations
		where domain = ?
	`, domain).Scan(&registration.Id, &registration.Domain, &registration.ByDid, &createdAt, &registeredAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		} else {
			return nil, err
		}
	}

	createdAtTime, _ := time.Parse(time.RFC3339, *createdAt)
	var registeredAtTime *time.Time
	if registeredAt != nil {
		x, _ := time.Parse(time.RFC3339, *registeredAt)
		registeredAtTime = &x
	}

	registration.Created = &createdAtTime
	registration.Registered = registeredAtTime

	return &registration, nil
}

func genSecret() string {
	key := make([]byte, 32)
	rand.Read(key)
	return hex.EncodeToString(key)
}

func GenerateRegistrationKey(e Execer, domain, did string) (string, error) {
	// sanity check: does this domain already have a registration?
	reg, err := RegistrationByDomain(e, domain)
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

	secret := genSecret()

	_, err = e.Exec(`
		insert into registrations (domain, did, secret)
		values (?, ?, ?)
		on conflict(domain) do update set did = excluded.did, secret = excluded.secret, created = excluded.created
		`, domain, did, secret)

	if err != nil {
		return "", err
	}

	return secret, nil
}

func GetRegistrationKey(e Execer, domain string) (string, error) {
	res := e.QueryRow(`select secret from registrations where domain = ?`, domain)

	var secret string
	err := res.Scan(&secret)
	if err != nil || secret == "" {
		return "", err
	}

	return secret, nil
}

func GetCompletedRegistrations(e Execer) ([]string, error) {
	rows, err := e.Query(`select domain from registrations where registered not null`)
	if err != nil {
		return nil, err
	}

	var domains []string
	for rows.Next() {
		var domain string
		err = rows.Scan(&domain)

		if err != nil {
			log.Println(err)
		} else {
			domains = append(domains, domain)
		}
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return domains, nil
}

func Register(e Execer, domain string) error {
	_, err := e.Exec(`
		update registrations
		set registered = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where domain = ?;
		`, domain)

	return err
}

func AddKnot(e Execer, domain, did string) error {
	_, err := e.Exec(`
		insert into registrations (domain, did)
		values (?, ?)
	`, domain, did)
	return err
}

func DeleteKnot(e Execer, filters ...filter) error {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`delete from registrations %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}
