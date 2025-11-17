package models

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
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

func (s *String) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", s.Did, tangled.StringNSID, s.Rkey))
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
