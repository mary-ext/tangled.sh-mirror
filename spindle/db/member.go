package db

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type SpindleMember struct {
	Id       int
	Did      syntax.DID // owner of the record
	Rkey     string     // rkey of the record
	Instance string
	Subject  syntax.DID // the member being added
	Created  time.Time
}

func AddSpindleMember(db *DB, member SpindleMember) error {
	_, err := db.Exec(
		`insert or ignore into spindle_members (did, rkey, instance, subject) values (?, ?, ?, ?)`,
		member.Did,
		member.Rkey,
		member.Instance,
		member.Subject,
	)
	return err
}

func RemoveSpindleMember(db *DB, owner_did, rkey string) error {
	_, err := db.Exec(
		"delete from spindle_members where did = ? and rkey = ?",
		owner_did,
		rkey,
	)
	return err
}

func GetSpindleMember(db *DB, did, rkey string) (*SpindleMember, error) {
	query :=
		`select id, did, rkey, instance, subject, created
		from spindle_members
		where did = ? and rkey = ?`

	var member SpindleMember
	var createdAt string
	err := db.QueryRow(query, did, rkey).Scan(
		&member.Id,
		&member.Did,
		&member.Rkey,
		&member.Instance,
		&member.Subject,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}

	return &member, nil
}
