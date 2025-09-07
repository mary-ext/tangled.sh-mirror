package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Spindle struct {
	Id           int
	Owner        syntax.DID
	Instance     string
	Verified     *time.Time
	Created      time.Time
	NeedsUpgrade bool
}

type SpindleMember struct {
	Id       int
	Did      syntax.DID // owner of the record
	Rkey     string     // rkey of the record
	Instance string
	Subject  syntax.DID // the member being added
	Created  time.Time
}

func GetSpindles(e Execer, filters ...filter) ([]Spindle, error) {
	var spindles []Spindle

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

	query := fmt.Sprintf(
		`select id, owner, instance, verified, created, needs_upgrade
		from spindles
		%s
		order by created
		`,
		whereClause,
	)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var spindle Spindle
		var createdAt string
		var verified sql.NullString
		var needsUpgrade int

		if err := rows.Scan(
			&spindle.Id,
			&spindle.Owner,
			&spindle.Instance,
			&verified,
			&createdAt,
			&needsUpgrade,
		); err != nil {
			return nil, err
		}

		spindle.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			spindle.Created = time.Now()
		}

		if verified.Valid {
			t, err := time.Parse(time.RFC3339, verified.String)
			if err != nil {
				now := time.Now()
				spindle.Verified = &now
			}
			spindle.Verified = &t
		}

		if needsUpgrade != 0 {
			spindle.NeedsUpgrade = true
		}

		spindles = append(spindles, spindle)
	}

	return spindles, nil
}

// if there is an existing spindle with the same instance, this returns an error
func AddSpindle(e Execer, spindle Spindle) error {
	_, err := e.Exec(
		`insert into spindles (owner, instance) values (?, ?)`,
		spindle.Owner,
		spindle.Instance,
	)
	return err
}

func VerifySpindle(e Execer, filters ...filter) (int64, error) {
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

	query := fmt.Sprintf(`update spindles set verified = strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now'), needs_upgrade = 0 %s`, whereClause)

	res, err := e.Exec(query, args...)
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func DeleteSpindle(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from spindles %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}

func AddSpindleMember(e Execer, member SpindleMember) error {
	_, err := e.Exec(
		`insert or ignore into spindle_members (did, rkey, instance, subject) values (?, ?, ?, ?)`,
		member.Did,
		member.Rkey,
		member.Instance,
		member.Subject,
	)
	return err
}

func RemoveSpindleMember(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from spindle_members %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}

func GetSpindleMembers(e Execer, filters ...filter) ([]SpindleMember, error) {
	var members []SpindleMember

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

	query := fmt.Sprintf(
		`select id, did, rkey, instance, subject, created
		from spindle_members
		%s
		order by created
		`,
		whereClause,
	)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var member SpindleMember
		var createdAt string

		if err := rows.Scan(
			&member.Id,
			&member.Did,
			&member.Rkey,
			&member.Instance,
			&member.Subject,
			&createdAt,
		); err != nil {
			return nil, err
		}

		member.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			member.Created = time.Now()
		}

		members = append(members, member)
	}

	return members, nil
}
