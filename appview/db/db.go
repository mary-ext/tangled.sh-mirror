package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

type Execer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

func Make(dbPath string) (*DB, error) {
	// https://github.com/mattn/go-sqlite3#connection-string
	opts := []string{
		"_foreign_keys=1",
		"_journal_mode=WAL",
		"_synchronous=NORMAL",
		"_auto_vacuum=incremental",
	}

	db, err := sql.Open("sqlite3", dbPath+"?"+strings.Join(opts, "&"))
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `
		create table if not exists registrations (
			id integer primary key autoincrement,
			domain text not null unique,
			did text not null,
			secret text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			registered text
		);
		create table if not exists public_keys (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			key text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, name, key)
		);
		create table if not exists repos (
			id integer primary key autoincrement,
			did text not null,
			name text not null,
			knot text not null,
			rkey text not null,
			at_uri text not null unique,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, name, knot, rkey)
		);
		create table if not exists collaborators (
			id integer primary key autoincrement,
			did text not null,
			repo integer not null,
			foreign key (repo) references repos(id) on delete cascade
		);
		create table if not exists follows (
			user_did text not null,
			subject_did text not null,
			rkey text not null,
			followed_at text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			primary key (user_did, subject_did),
			check (user_did <> subject_did)
		);
		create table if not exists issues (
			id integer primary key autoincrement,
			owner_did text not null,
			repo_at text not null,
			issue_id integer not null,
			title text not null,
			body text not null,
			open integer not null default 1,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			issue_at text,
			unique(repo_at, issue_id),
			foreign key (repo_at) references repos(at_uri) on delete cascade
		);
		create table if not exists comments (
			id integer primary key autoincrement,
			owner_did text not null,
			issue_id integer not null,
			repo_at text not null,
			comment_id integer not null,
			comment_at text not null,
			body text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(issue_id, comment_id),
			foreign key (repo_at, issue_id) references issues(repo_at, issue_id) on delete cascade
		);
		create table if not exists pulls (
			-- identifiers
			id integer primary key autoincrement,
			pull_id integer not null,

			-- at identifiers
			repo_at text not null,
			owner_did text not null,
			rkey text not null,
			pull_at text,

			-- content
			title text not null,
			body text not null,
			target_branch text not null,
			state integer not null default 0 check (state in (0, 1, 2)), -- open, merged, closed

			-- meta
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- constraints
			unique(repo_at, pull_id),
			foreign key (repo_at) references repos(at_uri) on delete cascade
		);

		-- every pull must have atleast 1 submission: the initial submission
		create table if not exists pull_submissions (
			-- identifiers
			id integer primary key autoincrement,
			pull_id integer not null,

			-- at identifiers
			repo_at text not null,

			-- content, these are immutable, and require a resubmission to update
			round_number integer not null default 0,
			patch text,

			-- meta
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- constraints
			unique(repo_at, pull_id, round_number),
			foreign key (repo_at, pull_id) references pulls(repo_at, pull_id) on delete cascade
		);

		create table if not exists pull_comments (
			-- identifiers
			id integer primary key autoincrement,
			pull_id integer not null,
			submission_id integer not null,

			-- at identifiers
			repo_at text not null,
			owner_did text not null,
			comment_at text not null,

			-- content
			body text not null,

			-- meta
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- constraints
			foreign key (repo_at, pull_id) references pulls(repo_at, pull_id) on delete cascade,
			foreign key (submission_id) references pull_submissions(id) on delete cascade
		);

		create table if not exists _jetstream (
			id integer primary key autoincrement,
			last_time_us integer not null
		);

		create table if not exists repo_issue_seqs (
			repo_at text primary key,
			next_issue_id integer not null default 1
		);

		create table if not exists repo_pull_seqs (
			repo_at text primary key,
			next_pull_id integer not null default 1
		);

		create table if not exists stars (
			id integer primary key autoincrement,
			starred_by_did text not null,
			repo_at text not null,
			rkey text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			foreign key (repo_at) references repos(at_uri) on delete cascade,
			unique(starred_by_did, repo_at)
		);

		create table if not exists reactions (
			id integer primary key autoincrement,
			reacted_by_did text not null,
			thread_at text not null,
			kind text not null,
			rkey text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(reacted_by_did, thread_at, kind)
		);

		create table if not exists emails (
			id integer primary key autoincrement,
			did text not null,
			email text not null,
			verified integer not null default 0,
			verification_code text not null,
			last_sent text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			is_primary integer not null default 0,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			unique(did, email)
		);

		create table if not exists artifacts (
			-- id
			id integer primary key autoincrement,
			did text not null,
			rkey text not null,

			-- meta
			repo_at text not null,
			tag binary(20) not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- data
			blob_cid text not null,
			name text not null,
			size integer not null default 0,
			mimetype string not null default "*/*",

			-- constraints
			unique(did, rkey),          -- record must be unique
			unique(repo_at, tag, name), -- for a given tag object, each file must be unique
			foreign key (repo_at) references repos(at_uri) on delete cascade
		);

		create table if not exists profile (
			-- id
			id integer primary key autoincrement,
			did text not null,

			-- data
			description text not null,
			include_bluesky integer not null default 0,
			location text,

			-- constraints
			unique(did)
		);
		create table if not exists profile_links (
			-- id
			id integer primary key autoincrement,
			did text not null,

			-- data
			link text not null,

			-- constraints
			foreign key (did) references profile(did) on delete cascade
		);
		create table if not exists profile_stats (
			-- id
			id integer primary key autoincrement,
			did text not null,

			-- data
			kind text not null check (kind in (
				"merged-pull-request-count",
				"closed-pull-request-count",
				"open-pull-request-count",
				"open-issue-count",
				"closed-issue-count",
				"repository-count"
			)),

			-- constraints
			foreign key (did) references profile(did) on delete cascade
		);
		create table if not exists profile_pinned_repositories (
			-- id
			id integer primary key autoincrement,
			did text not null,

			-- data
			at_uri text not null,

			-- constraints
			unique(did, at_uri),
			foreign key (did) references profile(did) on delete cascade,
			foreign key (at_uri) references repos(at_uri) on delete cascade
		);

		create table if not exists oauth_requests (
			id integer primary key autoincrement,
			auth_server_iss text not null,
			state text not null,
			did text not null,
			handle text not null,
			pds_url text not null,
			pkce_verifier text not null,
			dpop_auth_server_nonce text not null,
			dpop_private_jwk text not null
		);

		create table if not exists oauth_sessions (
			id integer primary key autoincrement,
			did text not null,
			handle text not null,
			pds_url text not null,
			auth_server_iss text not null,
			access_jwt text not null,
			refresh_jwt text not null,
			dpop_pds_nonce text,
			dpop_auth_server_nonce text not null,
			dpop_private_jwk text not null,
			expiry text not null
		);

		create table if not exists punchcard (
			did text not null,
			date text not null, -- yyyy-mm-dd
			count integer,
			primary key (did, date)
		);

		create table if not exists spindles (
			id integer primary key autoincrement,
			owner text not null,
			instance text not null,
			verified text, -- time of verification
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			unique(owner, instance)
		);

		create table if not exists spindle_members (
			-- identifiers for the record
			id integer primary key autoincrement,
			did text not null,
			rkey text not null,

			-- data
			instance text not null,
			subject text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- constraints
			unique (did, instance, subject)
		);

		create table if not exists pipelines (
			-- identifiers
			id integer primary key autoincrement,
			knot text not null,
			rkey text not null,

			repo_owner text not null,
			repo_name text not null,

			-- every pipeline must be associated with exactly one commit
			sha text not null check (length(sha) = 40),
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

			-- trigger data
			trigger_id integer not null,

			unique(knot, rkey),
			foreign key (trigger_id) references triggers(id) on delete cascade
		);

		create table if not exists triggers (
			-- primary key
			id integer primary key autoincrement,

			-- top-level fields
			kind text not null,

			-- pushTriggerData fields
			push_ref text,
			push_new_sha text check (length(push_new_sha) = 40),
			push_old_sha text check (length(push_old_sha) = 40),

			-- pullRequestTriggerData fields
			pr_source_branch text,
			pr_target_branch text,
			pr_source_sha text check (length(pr_source_sha) = 40),
			pr_action text
		);

		create table if not exists pipeline_statuses (
			-- identifiers
			id integer primary key autoincrement,
			spindle text not null,
			rkey text not null,

			-- referenced pipeline. these form the (did, rkey) pair
			pipeline_knot text not null,
			pipeline_rkey text not null,

			-- content
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			workflow text not null,
			status text not null,
			error text,
			exit_code integer not null default 0,

			unique (spindle, rkey),
			foreign key (pipeline_knot, pipeline_rkey)
				references pipelines (knot, rkey)
				on delete cascade
		);

		create table if not exists repo_languages (
			-- identifiers
			id integer primary key autoincrement,

			-- repo identifiers
			repo_at text not null,
			ref text not null,
			is_default_ref integer not null default 0,

			-- language breakdown
			language text not null,
			bytes integer not null check (bytes >= 0),

			unique(repo_at, ref, language)
		);

		create table if not exists signups_inflight (
			id integer primary key autoincrement,
			email text not null unique,
			invite_code text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);

		create table if not exists strings (
			-- identifiers
			did text not null,
			rkey text not null,

			-- content
			filename text not null,
			description text,
			content text not null,
			created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			edited text,

			primary key (did, rkey)
		);

		create table if not exists migrations (
			id integer primary key autoincrement,
			name text unique
		);

		-- indexes for better star query performance
		create index if not exists idx_stars_created on stars(created);
		create index if not exists idx_stars_repo_at_created on stars(repo_at, created);
	`)
	if err != nil {
		return nil, err
	}

	// run migrations
	runMigration(conn, "add-description-to-repos", func(tx *sql.Tx) error {
		tx.Exec(`
			alter table repos add column description text check (length(description) <= 200);
		`)
		return nil
	})

	runMigration(conn, "add-rkey-to-pubkeys", func(tx *sql.Tx) error {
		// add unconstrained column
		_, err := tx.Exec(`
			alter table public_keys
			add column rkey text;
		`)
		if err != nil {
			return err
		}

		// backfill
		_, err = tx.Exec(`
			update public_keys
			set rkey = ''
			where rkey is null;
		`)
		if err != nil {
			return err
		}

		return nil
	})

	runMigration(conn, "add-rkey-to-comments", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table comments drop column comment_at;
			alter table comments add column rkey text;
		`)
		return err
	})

	runMigration(conn, "add-deleted-and-edited-to-issue-comments", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table comments add column deleted text; -- timestamp
			alter table comments add column edited text; -- timestamp
		`)
		return err
	})

	runMigration(conn, "add-source-info-to-pulls-and-submissions", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table pulls add column source_branch text;
			alter table pulls add column source_repo_at text;
			alter table pull_submissions add column source_rev text;
		`)
		return err
	})

	runMigration(conn, "add-source-to-repos", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table repos add column source text;
		`)
		return err
	})

	// disable foreign-keys for the next migration
	// NOTE: this cannot be done in a transaction, so it is run outside [0]
	//
	// [0]: https://sqlite.org/pragma.html#pragma_foreign_keys
	conn.ExecContext(ctx, "pragma foreign_keys = off;")
	runMigration(conn, "recreate-pulls-column-for-stacking-support", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			create table pulls_new (
				-- identifiers
				id integer primary key autoincrement,
				pull_id integer not null,

				-- at identifiers
				repo_at text not null,
				owner_did text not null,
				rkey text not null,

				-- content
				title text not null,
				body text not null,
				target_branch text not null,
				state integer not null default 0 check (state in (0, 1, 2, 3)), -- closed, open, merged, deleted

				-- source info
				source_branch text,
				source_repo_at text,

				-- stacking
				stack_id text,
				change_id text,
				parent_change_id text,

				-- meta
				created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

				-- constraints
				unique(repo_at, pull_id),
				foreign key (repo_at) references repos(at_uri) on delete cascade
			);

			insert into pulls_new (
				id, pull_id,
				repo_at, owner_did, rkey,
				title, body, target_branch, state,
				source_branch, source_repo_at,
				created
			)
			select
				id, pull_id,
				repo_at, owner_did, rkey,
				title, body, target_branch, state,
				source_branch, source_repo_at,
				created
			FROM pulls;

			drop table pulls;
			alter table pulls_new rename to pulls;
		`)
		return err
	})
	conn.ExecContext(ctx, "pragma foreign_keys = on;")

	// run migrations
	runMigration(conn, "add-spindle-to-repos", func(tx *sql.Tx) error {
		tx.Exec(`
			alter table repos add column spindle text;
		`)
		return nil
	})

	// drop all knot secrets, add unique constraint to knots
	//
	// knots will henceforth use service auth for signed requests
	runMigration(conn, "no-more-secrets", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			create table registrations_new (
				id integer primary key autoincrement,
				domain text not null,
				did text not null,
				created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				registered text,
				read_only integer not null default 0,
				unique(domain, did)
			);

			insert into registrations_new (id, domain, did, created, registered, read_only)
			select id, domain, did, created, registered, 1 from registrations
			where registered is not null;

			drop table registrations;
			alter table registrations_new rename to registrations;
		`)
		return err
	})

	// recreate and add rkey + created columns with default constraint
	runMigration(conn, "rework-collaborators-table", func(tx *sql.Tx) error {
		// create new table
		// - repo_at instead of repo integer
		// - rkey field
		// - created field
		_, err := tx.Exec(`
			create table collaborators_new (
				-- identifiers for the record
				id integer primary key autoincrement,
				did text not null,
				rkey text,

				-- content
				subject_did text not null,
				repo_at text not null,

				-- meta
				created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

				-- constraints
				foreign key (repo_at) references repos(at_uri) on delete cascade
			)
		`)
		if err != nil {
			return err
		}

		// copy data
		_, err = tx.Exec(`
			insert into collaborators_new (id, did, rkey, subject_did, repo_at)
			select
				c.id,
				r.did,
				'',
				c.did,
				r.at_uri
			from collaborators c
			join repos r on c.repo = r.id
		`)
		if err != nil {
			return err
		}

		// drop old table
		_, err = tx.Exec(`drop table collaborators`)
		if err != nil {
			return err
		}

		// rename new table
		_, err = tx.Exec(`alter table collaborators_new rename to collaborators`)
		return err
	})

	runMigration(conn, "add-rkey-to-issues", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table issues add column rkey text not null default '';

			-- get last url section from issue_at and save to rkey column
			update issues
			set rkey = replace(issue_at, rtrim(issue_at, replace(issue_at, '/', '')), '');
		`)
		return err
	})

	// repurpose the read-only column to "needs-upgrade"
	runMigration(conn, "rename-registrations-read-only-to-needs-upgrade", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table registrations rename column read_only to needs_upgrade;
		`)
		return err
	})

	// require all knots to upgrade after the release of total xrpc
	runMigration(conn, "migrate-knots-to-total-xrpc", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			update registrations set needs_upgrade = 1;
		`)
		return err
	})

	// require all knots to upgrade after the release of total xrpc
	runMigration(conn, "migrate-spindles-to-xrpc-owner", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			alter table spindles add column needs_upgrade integer not null default 0;
		`)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`
			update spindles set needs_upgrade = 1;
		`)
		return err
	})

	// remove issue_at from issues and replace with generated column
	//
	// this requires a full table recreation because stored columns
	// cannot be added via alter
	//
	// couple other changes:
	// - columns renamed to be more consistent
	// - adds edited and deleted fields
	//
	// disable foreign-keys for the next migration
	conn.ExecContext(ctx, "pragma foreign_keys = off;")
	runMigration(conn, "remove-issue-at-from-issues", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			create table if not exists issues_new (
				-- identifiers
				id integer primary key autoincrement,
				did text not null,
				rkey text not null,
				at_uri text generated always as ('at://' || did || '/' || 'sh.tangled.repo.issue' || '/' || rkey) stored,

				-- at identifiers
				repo_at text not null,

				-- content
				issue_id integer not null,
				title text not null,
				body text not null,
				open integer not null default 1,
				created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				edited text,  -- timestamp
				deleted text,  -- timestamp

				unique(did, rkey),
				unique(repo_at, issue_id),
				unique(at_uri),
				foreign key (repo_at) references repos(at_uri) on delete cascade
			);
		`)
		if err != nil {
			return err
		}

		// transfer data
		_, err = tx.Exec(`
			insert into issues_new (id, did, rkey, repo_at, issue_id, title, body, open, created)
			select
				i.id,
				i.owner_did,
				i.rkey,
				i.repo_at,
				i.issue_id,
				i.title,
				i.body,
				i.open,
				i.created
			from issues i;
		`)
		if err != nil {
			return err
		}

		// drop old table
		_, err = tx.Exec(`drop table issues`)
		if err != nil {
			return err
		}

		// rename new table
		_, err = tx.Exec(`alter table issues_new rename to issues`)
		return err
	})
	conn.ExecContext(ctx, "pragma foreign_keys = on;")

	// - renames the comments table to 'issue_comments'
	// - rework issue comments to update constraints:
	//   * unique(did, rkey)
	//   * remove comment-id and just use the global ID
	//   * foreign key (repo_at, issue_id)
	// - new columns
	//   * column "reply_to" which can be any other comment
	//   * column "at-uri" which is a generated column
	runMigration(conn, "rework-issue-comments", func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			create table if not exists issue_comments (
				-- identifiers
				id integer primary key autoincrement,
				did text not null,
				rkey text,
				at_uri text generated always as ('at://' || did || '/' || 'sh.tangled.repo.issue.comment' || '/' || rkey) stored,

				-- at identifiers
				issue_at text not null,
				reply_to text, -- at_uri of parent comment

				-- content
				body text not null,
				created text not null default (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				edited text,
				deleted text,

				-- constraints
				unique(did, rkey),
				unique(at_uri),
				foreign key (issue_at) references issues(at_uri) on delete cascade
			);
		`)
		if err != nil {
			return err
		}

		// transfer data
		_, err = tx.Exec(`
			insert into issue_comments (id, did, rkey, issue_at, body, created, edited, deleted)
			select
				c.id,
				c.owner_did,
				c.rkey,
				i.at_uri,  -- get at_uri from issues table
				c.body,
				c.created,
				c.edited,
				c.deleted
			from comments c
			join issues i on c.repo_at = i.repo_at and c.issue_id = i.issue_id;
		`)
		if err != nil {
			return err
		}

		// drop old table
		_, err = tx.Exec(`drop table comments`)
		return err
	})

	return &DB{db}, nil
}

type migrationFn = func(*sql.Tx) error

func runMigration(c *sql.Conn, name string, migrationFn migrationFn) error {
	tx, err := c.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	err = tx.QueryRow("select exists (select 1 from migrations where name = ?)", name).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		// run migration
		err = migrationFn(tx)
		if err != nil {
			log.Printf("Failed to run migration %s: %v", name, err)
			return err
		}

		// mark migration as complete
		_, err = tx.Exec("insert into migrations (name) values (?)", name)
		if err != nil {
			log.Printf("Failed to mark migration %s as complete: %v", name, err)
			return err
		}

		// commit the transaction
		if err := tx.Commit(); err != nil {
			return err
		}

		log.Printf("migration %s applied successfully", name)
	} else {
		log.Printf("skipped migration %s, already applied", name)
	}

	return nil
}

func (d *DB) Close() error {
	return d.DB.Close()
}

type filter struct {
	key string
	arg any
	cmp string
}

func newFilter(key, cmp string, arg any) filter {
	return filter{
		key: key,
		arg: arg,
		cmp: cmp,
	}
}

func FilterEq(key string, arg any) filter    { return newFilter(key, "=", arg) }
func FilterNotEq(key string, arg any) filter { return newFilter(key, "<>", arg) }
func FilterGte(key string, arg any) filter   { return newFilter(key, ">=", arg) }
func FilterLte(key string, arg any) filter   { return newFilter(key, "<=", arg) }
func FilterIs(key string, arg any) filter    { return newFilter(key, "is", arg) }
func FilterIsNot(key string, arg any) filter { return newFilter(key, "is not", arg) }
func FilterIn(key string, arg any) filter    { return newFilter(key, "in", arg) }

func (f filter) Condition() string {
	rv := reflect.ValueOf(f.arg)
	kind := rv.Kind()

	// if we have `FilterIn(k, [1, 2, 3])`, compile it down to `k in (?, ?, ?)`
	if (kind == reflect.Slice && rv.Type().Elem().Kind() != reflect.Uint8) || kind == reflect.Array {
		if rv.Len() == 0 {
			// always false
			return "1 = 0"
		}

		placeholders := make([]string, rv.Len())
		for i := range placeholders {
			placeholders[i] = "?"
		}

		return fmt.Sprintf("%s %s (%s)", f.key, f.cmp, strings.Join(placeholders, ", "))
	}

	return fmt.Sprintf("%s %s ?", f.key, f.cmp)
}

func (f filter) Arg() []any {
	rv := reflect.ValueOf(f.arg)
	kind := rv.Kind()
	if (kind == reflect.Slice && rv.Type().Elem().Kind() != reflect.Uint8) || kind == reflect.Array {
		if rv.Len() == 0 {
			return nil
		}

		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = rv.Index(i).Interface()
		}
		return out
	}

	return []any{f.arg}
}
