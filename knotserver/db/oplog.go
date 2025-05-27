package db

import (
	"fmt"
)

type Op struct {
	Tid    string // time based ID, easy to enumerate & monotonic
	Did    string // did of pusher
	Repo   string // <did/repo> fully qualified repo
	OldSha string // old sha of reference being updated
	NewSha string // new sha of reference being updated
	Ref    string // the reference being updated
}

func (d *DB) InsertOp(op Op) error {
	_, err := d.db.Exec(
		`insert into oplog (tid, did, repo, old_sha, new_sha, ref) values (?, ?, ?, ?, ?, ?)`,
		op.Tid,
		op.Did,
		op.Repo,
		op.OldSha,
		op.NewSha,
		op.Ref,
	)
	return err
}

func (d *DB) GetOps(cursor string) ([]Op, error) {
	whereClause := ""
	args := []any{}
	if cursor != "" {
		whereClause = "where tid > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select tid, did, repo, old_sha, new_sha, ref
		from oplog
		%s
		order by tid asc
		limit 100
	`, whereClause)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ops []Op
	for rows.Next() {
		var op Op
		rows.Scan(&op.Tid, &op.Did, &op.Repo, &op.OldSha, &op.NewSha, &op.Ref)
		ops = append(ops, op)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ops, nil
}
