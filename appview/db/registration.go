package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Registration represents a knot registration. Knot would've been a better
// name but we're stuck with this for historical reasons.
type Registration struct {
	Id           int64
	Domain       string
	ByDid        string
	Created      *time.Time
	Registered   *time.Time
	NeedsUpgrade bool
}

func (r *Registration) Status() Status {
	if r.NeedsUpgrade {
		return NeedsUpgrade
	} else if r.Registered != nil {
		return Registered
	} else {
		return Pending
	}
}

func (r *Registration) IsRegistered() bool {
	return r.Status() == Registered
}

func (r *Registration) IsNeedsUpgrade() bool {
	return r.Status() == NeedsUpgrade
}

func (r *Registration) IsPending() bool {
	return r.Status() == Pending
}

type Status uint32

const (
	Registered Status = iota
	Pending
	NeedsUpgrade
)

func GetRegistrations(e Execer, filters ...filter) ([]Registration, error) {
	var registrations []Registration

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

	query := fmt.Sprintf(`
		select id, domain, did, created, registered, needs_upgrade
		from registrations
		%s
		order by created
		`,
		whereClause,
	)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var createdAt string
		var registeredAt sql.Null[string]
		var needsUpgrade int
		var reg Registration

		err = rows.Scan(&reg.Id, &reg.Domain, &reg.ByDid, &createdAt, &registeredAt, &needsUpgrade)
		if err != nil {
			return nil, err
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			reg.Created = &t
		}

		if registeredAt.Valid {
			if t, err := time.Parse(time.RFC3339, registeredAt.V); err == nil {
				reg.Registered = &t
			}
		}

		if needsUpgrade != 0 {
			reg.NeedsUpgrade = true
		}

		registrations = append(registrations, reg)
	}

	return registrations, nil
}

func MarkRegistered(e Execer, filters ...filter) error {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	query := "update registrations set registered = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), needs_upgrade = 0"
	if len(conditions) > 0 {
		query += " where " + strings.Join(conditions, " and ")
	}

	_, err := e.Exec(query, args...)
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
