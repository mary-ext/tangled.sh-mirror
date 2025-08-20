package db

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/api/tangled"
)

type String struct {
	Did  syntax.DID
	Rkey string

	Filename    string
	Description string
	Contents    string
	Created     time.Time
	Edited      *time.Time
}

func (s *String) StringAt() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", s.Did, tangled.StringNSID, s.Rkey))
}

type StringStats struct {
	LineCount uint64
	ByteCount uint64
}

func (s String) Stats() StringStats {
	lineCount, err := countLines(strings.NewReader(s.Contents))
	if err != nil {
		// non-fatal
		// TODO: log this?
	}

	return StringStats{
		LineCount: uint64(lineCount),
		ByteCount: uint64(len(s.Contents)),
	}
}

func (s String) Validate() error {
	var err error

	if utf8.RuneCountInString(s.Filename) > 140 {
		err = errors.Join(err, fmt.Errorf("filename too long"))
	}

	if utf8.RuneCountInString(s.Description) > 280 {
		err = errors.Join(err, fmt.Errorf("description too long"))
	}

	if len(s.Contents) == 0 {
		err = errors.Join(err, fmt.Errorf("contents is empty"))
	}

	return err
}

func (s *String) AsRecord() tangled.String {
	return tangled.String{
		Filename:    s.Filename,
		Description: s.Description,
		Contents:    s.Contents,
		CreatedAt:   s.Created.Format(time.RFC3339),
	}
}

func StringFromRecord(did, rkey string, record tangled.String) String {
	created, err := time.Parse(record.CreatedAt, time.RFC3339)
	if err != nil {
		created = time.Now()
	}
	return String{
		Did:         syntax.DID(did),
		Rkey:        rkey,
		Filename:    record.Filename,
		Description: record.Description,
		Contents:    record.Contents,
		Created:     created,
	}
}

func AddString(e Execer, s String) error {
	_, err := e.Exec(
		`insert into strings (
			did,
			rkey,
			filename,
			description,
			content,
			created,
			edited
		)
		values (?, ?, ?, ?, ?, ?, null)
		on conflict(did, rkey) do update set
			filename = excluded.filename,
			description = excluded.description,
			content = excluded.content,
			edited = case
				when
					strings.content != excluded.content
					or strings.filename != excluded.filename
					or strings.description != excluded.description then ?
				else strings.edited
			end`,
		s.Did,
		s.Rkey,
		s.Filename,
		s.Description,
		s.Contents,
		s.Created.Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)
	return err
}

func GetStrings(e Execer, limit int, filters ...filter) ([]String, error) {
	var all []String

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

	limitClause := ""
	if limit != 0 {
		limitClause = fmt.Sprintf(" limit %d ", limit)
	}

	query := fmt.Sprintf(`select
			did,
			rkey,
			filename,
			description,
			content,
			created,
			edited
		from strings
		%s
		order by created desc
		%s`,
		whereClause,
		limitClause,
	)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s String
		var createdAt string
		var editedAt sql.NullString

		if err := rows.Scan(
			&s.Did,
			&s.Rkey,
			&s.Filename,
			&s.Description,
			&s.Contents,
			&createdAt,
			&editedAt,
		); err != nil {
			return nil, err
		}

		s.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			s.Created = time.Now()
		}

		if editedAt.Valid {
			e, err := time.Parse(time.RFC3339, editedAt.String)
			if err != nil {
				e = time.Now()
			}
			s.Edited = &e
		}

		all = append(all, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return all, nil
}

func DeleteString(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from strings %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}

func countLines(r io.Reader) (int, error) {
	buf := make([]byte, 32*1024)
	bufLen := 0
	count := 0
	nl := []byte{'\n'}

	for {
		c, err := r.Read(buf)
		if c > 0 {
			bufLen += c
		}
		count += bytes.Count(buf[:c], nl)

		switch {
		case err == io.EOF:
			/* handle last line not having a newline at the end */
			if bufLen >= 1 && buf[(bufLen-1)%(32*1024)] != '\n' {
				count++
			}
			return count, nil
		case err != nil:
			return 0, err
		}
	}
}
