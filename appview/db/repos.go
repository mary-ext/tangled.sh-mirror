package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Repo struct {
	Did         string
	Name        string
	Knot        string
	Rkey        string
	Created     time.Time
	AtUri       string
	Description string

	// optionally, populate this when querying for reverse mappings
	RepoStats *RepoStats

	// optional
	Source string
}

func GetAllRepos(ctx context.Context, e Execer, limit int) ([]Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetAllRepos")
	defer span.End()
	span.SetAttributes(attribute.Int("limit", limit))

	var repos []Repo

	rows, err := e.Query(
		`select did, name, knot, rkey, description, created, source
		from repos
		order by created desc
		limit ?
		`,
		limit,
	)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		err := scanRepo(
			rows, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &repo.Description, &repo.Created, &repo.Source,
		)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}
		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("repos.count", len(repos)))
	return repos, nil
}

func GetAllReposByDid(ctx context.Context, e Execer, did string) ([]Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetAllReposByDid")
	defer span.End()
	span.SetAttributes(attribute.String("did", did))

	var repos []Repo

	rows, err := e.Query(
		`select
			r.did,
			r.name,
			r.knot,
			r.rkey,
			r.description,
			r.created,
			count(s.id) as star_count,
			r.source
		from
			repos r
		left join
			stars s on r.at_uri = s.repo_at
		where
			r.did = ?
		group by
			r.at_uri
		order by r.created desc`,
		did)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var repoStats RepoStats
		var createdAt string
		var nullableDescription sql.NullString
		var nullableSource sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repoStats.StarCount, &nullableSource)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		}

		if nullableSource.Valid {
			repo.Source = nullableSource.String
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repo.RepoStats = &repoStats

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("repos.count", len(repos)))
	return repos, nil
}

func GetRepo(ctx context.Context, e Execer, did, name string) (*Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetRepo")
	defer span.End()
	span.SetAttributes(
		attribute.String("did", did),
		attribute.String("name", name),
	)

	var repo Repo
	var nullableDescription sql.NullString

	row := e.QueryRow(`select did, name, knot, created, at_uri, description from repos where did = ? and name = ?`, did, name)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.AtUri, &nullableDescription); err != nil {
		span.RecordError(err)
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = createdAtTime

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	} else {
		repo.Description = ""
	}

	return &repo, nil
}

func GetRepoByAtUri(ctx context.Context, e Execer, atUri string) (*Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetRepoByAtUri")
	defer span.End()
	span.SetAttributes(attribute.String("atUri", atUri))

	var repo Repo
	var nullableDescription sql.NullString

	row := e.QueryRow(`select did, name, knot, created, at_uri, description from repos where at_uri = ?`, atUri)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.AtUri, &nullableDescription); err != nil {
		span.RecordError(err)
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = createdAtTime

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	} else {
		repo.Description = ""
	}

	return &repo, nil
}

func AddRepo(ctx context.Context, e Execer, repo *Repo) error {
	ctx, span := otel.Tracer("db").Start(ctx, "AddRepo")
	defer span.End()
	span.SetAttributes(
		attribute.String("did", repo.Did),
		attribute.String("name", repo.Name),
	)

	_, err := e.Exec(
		`insert into repos
		(did, name, knot, rkey, at_uri, description, source)
		values (?, ?, ?, ?, ?, ?, ?)`,
		repo.Did, repo.Name, repo.Knot, repo.Rkey, repo.AtUri, repo.Description, repo.Source,
	)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func RemoveRepo(ctx context.Context, e Execer, did, name string) error {
	ctx, span := otel.Tracer("db").Start(ctx, "RemoveRepo")
	defer span.End()
	span.SetAttributes(
		attribute.String("did", did),
		attribute.String("name", name),
	)

	_, err := e.Exec(`delete from repos where did = ? and name = ?`, did, name)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func GetRepoSource(ctx context.Context, e Execer, repoAt syntax.ATURI) (string, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetRepoSource")
	defer span.End()
	span.SetAttributes(attribute.String("repoAt", repoAt.String()))

	var nullableSource sql.NullString
	err := e.QueryRow(`select source from repos where at_uri = ?`, repoAt).Scan(&nullableSource)
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	return nullableSource.String, nil
}

func GetForksByDid(ctx context.Context, e Execer, did string) ([]Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetForksByDid")
	defer span.End()
	span.SetAttributes(attribute.String("did", did))

	var repos []Repo

	rows, err := e.Query(
		`select did, name, knot, rkey, description, created, at_uri, source
		from repos
		where did = ? and source is not null and source != ''
		order by created desc`,
		did,
	)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var createdAt string
		var nullableDescription sql.NullString
		var nullableSource sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repo.AtUri, &nullableSource)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		}

		if nullableSource.Valid {
			repo.Source = nullableSource.String
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("forks.count", len(repos)))
	return repos, nil
}

func GetForkByDid(ctx context.Context, e Execer, did string, name string) (*Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "GetForkByDid")
	defer span.End()
	span.SetAttributes(
		attribute.String("did", did),
		attribute.String("name", name),
	)

	var repo Repo
	var createdAt string
	var nullableDescription sql.NullString
	var nullableSource sql.NullString

	row := e.QueryRow(
		`select did, name, knot, rkey, description, created, at_uri, source
		from repos
		where did = ? and name = ? and source is not null and source != ''`,
		did, name,
	)

	err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repo.AtUri, &nullableSource)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	}

	if nullableSource.Valid {
		repo.Source = nullableSource.String
	}

	createdAtTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		repo.Created = time.Now()
	} else {
		repo.Created = createdAtTime
	}

	return &repo, nil
}

func AddCollaborator(ctx context.Context, e Execer, collaborator, repoOwnerDid, repoName, repoKnot string) error {
	ctx, span := otel.Tracer("db").Start(ctx, "AddCollaborator")
	defer span.End()
	span.SetAttributes(
		attribute.String("collaborator", collaborator),
		attribute.String("repoOwnerDid", repoOwnerDid),
		attribute.String("repoName", repoName),
	)

	_, err := e.Exec(
		`insert into collaborators (did, repo)
		values (?, (select id from repos where did = ? and name = ? and knot = ?));`,
		collaborator, repoOwnerDid, repoName, repoKnot)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func UpdateDescription(ctx context.Context, e Execer, repoAt, newDescription string) error {
	ctx, span := otel.Tracer("db").Start(ctx, "UpdateDescription")
	defer span.End()
	span.SetAttributes(
		attribute.String("repoAt", repoAt),
		attribute.String("description", newDescription),
	)

	_, err := e.Exec(
		`update repos set description = ? where at_uri = ?`, newDescription, repoAt)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func CollaboratingIn(ctx context.Context, e Execer, collaborator string) ([]Repo, error) {
	ctx, span := otel.Tracer("db").Start(ctx, "CollaboratingIn")
	defer span.End()
	span.SetAttributes(attribute.String("collaborator", collaborator))

	var repos []Repo

	rows, err := e.Query(
		`select
			r.did, r.name, r.knot, r.rkey, r.description, r.created, count(s.id) as star_count
		from
			repos r
		join
			collaborators c on r.id = c.repo
		left join
			stars s on r.at_uri = s.repo_at
		where
			c.did = ?
		group by
			r.id;`, collaborator)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var repoStats RepoStats
		var createdAt string
		var nullableDescription sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repoStats.StarCount)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		} else {
			repo.Description = ""
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repo.RepoStats = &repoStats

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("repos.count", len(repos)))
	return repos, nil
}

type RepoStats struct {
	StarCount  int
	IssueCount IssueCount
	PullCount  PullCount
}

func scanRepo(rows *sql.Rows, did, name, knot, rkey, description *string, created *time.Time, source *string) error {
	var createdAt string
	var nullableDescription sql.NullString
	var nullableSource sql.NullString
	if err := rows.Scan(did, name, knot, rkey, &nullableDescription, &createdAt, &nullableSource); err != nil {
		return err
	}

	if nullableDescription.Valid {
		*description = nullableDescription.String
	} else {
		*description = ""
	}

	createdAtTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		*created = time.Now()
	} else {
		*created = createdAtTime
	}

	if nullableSource.Valid {
		*source = nullableSource.String
	} else {
		*source = ""
	}

	return nil
}
