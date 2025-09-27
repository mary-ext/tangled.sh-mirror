package git

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func (g *GitRepo) Tags() ([]object.Tag, error) {
	fields := []string{
		"refname:short",
		"objectname",
		"objecttype",
		"*objectname",
		"*objecttype",
		"taggername",
		"taggeremail",
		"taggerdate:unix",
		"contents",
	}

	var outFormat strings.Builder
	outFormat.WriteString("--format=")
	for i, f := range fields {
		if i != 0 {
			outFormat.WriteString(fieldSeparator)
		}
		outFormat.WriteString(fmt.Sprintf("%%(%s)", f))
	}
	outFormat.WriteString("")
	outFormat.WriteString(recordSeparator)

	output, err := g.forEachRef(outFormat.String(), "--sort=-creatordate", "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}

	records := strings.Split(strings.TrimSpace(string(output)), recordSeparator)
	if len(records) == 1 && records[0] == "" {
		return nil, nil
	}

	tags := make([]object.Tag, 0, len(records))

	for _, line := range records {
		parts := strings.SplitN(strings.TrimSpace(line), fieldSeparator, len(fields))
		if len(parts) < 6 {
			continue
		}

		tagName := parts[0]
		objectHash := parts[1]
		objectType := parts[2]
		targetHash := parts[3] // dereferenced object hash (empty for lightweight tags)
		// targetType := parts[4] // dereferenced object type (empty for lightweight tags)
		taggerName := parts[5]
		taggerEmail := parts[6]
		taggerDate := parts[7]
		message := parts[8]

		// parse creation time
		var createdAt time.Time
		if unix, err := strconv.ParseInt(taggerDate, 10, 64); err == nil {
			createdAt = time.Unix(unix, 0)
		}

		// parse object type
		typ, err := plumbing.ParseObjectType(objectType)
		if err != nil {
			return nil, err
		}

		// strip email separators
		taggerEmail = strings.TrimSuffix(strings.TrimPrefix(taggerEmail, "<"), ">")

		tag := object.Tag{
			Hash: plumbing.NewHash(objectHash),
			Name: tagName,
			Tagger: object.Signature{
				Name:  taggerName,
				Email: taggerEmail,
				When:  createdAt,
			},
			Message:    message,
			TargetType: typ,
			Target:     plumbing.NewHash(targetHash),
		}

		tags = append(tags, tag)
	}

	return tags, nil
}
