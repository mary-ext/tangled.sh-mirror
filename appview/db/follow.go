package db

import (
	"log"
	"time"
)

type Follow struct {
	UserDid    string
	SubjectDid string
	FollowedAt *time.Time
	AtUri      string
}

func (d *DB) AddFollow(userDid, subjectDid, atUri string) error {
	query := `insert into follows (user_did, subject_did, at_uri) values (?, ?, ?)`
	_, err := d.db.Exec(query, userDid, subjectDid, atUri)
	return err
}

// Get a follow record
func (d *DB) GetFollow(userDid, subjectDid string) (*Follow, error) {
	query := `select user_did, subject_did, followed_at, at_uri from follows where user_did = ? and subject_did = ?`
	row := d.db.QueryRow(query, userDid, subjectDid)

	var follow Follow
	var followedAt string
	err := row.Scan(&follow.UserDid, &follow.SubjectDid, &followedAt, &follow.AtUri)
	if err != nil {
		return nil, err
	}

	followedAtTime, err := time.Parse(time.RFC3339, followedAt)
	if err != nil {
		log.Println("unable to determine followed at time")
		follow.FollowedAt = nil
	} else {
		follow.FollowedAt = &followedAtTime
	}

	return &follow, nil
}

// Get a follow record
func (d *DB) DeleteFollow(userDid, subjectDid string) error {
	_, err := d.db.Exec(`delete from follows where user_did = ? and subject_did = ?`, userDid, subjectDid)
	return err
}
