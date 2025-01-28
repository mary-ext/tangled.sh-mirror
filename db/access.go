package db

import (
	"log"
	"strings"
)

// forms a poset
type Level int

const (
	Reader Level = iota
	Writer
	Owner
)

var (
	levelMap = map[string]Level{
		"writer": Writer,
		"owner":  Owner,
	}
)

func ParseLevel(str string) (Level, bool) {
	c, ok := levelMap[strings.ToLower(str)]
	return c, ok
}

func (l Level) String() string {
	switch l {
	case Owner:
		return "OWNER"
	case Writer:
		return "WRITER"
	case Reader:
		return "READER"
	default:
		return "READER"
	}
}

func (d *DB) SetAccessLevel(userDid string, repoDid string, repoName string, level Level) error {
	_, err := d.db.Exec(
		`insert
		into access_levels (repo_id, did, access)
		values ((select id from repos where did = $1 and name = $2), $3, $4)
		on conflict (repo_id, did)
		do update set access = $4;`,
		repoDid, repoName, userDid, level.String())
	return err
}

func (d *DB) SetOwner(userDid string, repoDid string, repoName string) error {
	return d.SetAccessLevel(userDid, repoDid, repoName, Owner)
}

func (d *DB) SetWriter(userDid string, repoDid string, repoName string) error {
	return d.SetAccessLevel(userDid, repoDid, repoName, Writer)
}

func (d *DB) GetAccessLevel(userDid string, repoDid string, repoName string) (Level, error) {
	row := d.db.QueryRow(`
		select access_levels.access
		from repos
		join access_levels
		on repos.id = access_levels.repo_id
		where access_levels.did = ? and repos.did = ? and repos.name = ?
	`, userDid, repoDid, repoName)

	var levelStr string
	err := row.Scan(&levelStr)
	if err != nil {
		log.Println(err)
		return Reader, err
	} else {
		level, _ := ParseLevel(levelStr)
		return level, nil
	}

}
