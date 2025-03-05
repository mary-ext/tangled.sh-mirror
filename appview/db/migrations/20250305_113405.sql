-- Simplified SQLite Database Migration Script for Issues and Comments

-- Migration for issues table
CREATE TABLE issues_new (
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

-- Migrate data to new issues table
INSERT INTO issues_new (
    id, owner_did, repo_at, issue_id,
    title, body, open, created, issue_at
)
SELECT
    id, owner_did, repo_at, issue_id,
    title, body, open, created, issue_at
FROM issues;

-- Drop old issues table
DROP TABLE issues;

-- Rename new issues table
ALTER TABLE issues_new RENAME TO issues;

-- Migration for comments table
CREATE TABLE comments_new (
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

-- Migrate data to new comments table
INSERT INTO comments_new (
    id, owner_did, issue_id, repo_at,
    comment_id, comment_at, body, created
)
SELECT
    id, owner_did, issue_id, repo_at,
    comment_id, comment_at, body, created
FROM comments;

-- Drop old comments table
DROP TABLE comments;

-- Rename new comments table
ALTER TABLE comments_new RENAME TO comments;
