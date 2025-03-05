-- Validation Queries for Database Migration

-- 1. Verify Issues Table Structure
PRAGMA table_info(issues);

-- 2. Verify Comments Table Structure
PRAGMA table_info(comments);

-- 3. Check Total Row Count Consistency
SELECT
    'Issues Row Count' AS check_type,
    (SELECT COUNT(*) FROM issues) AS row_count
UNION ALL
SELECT
    'Comments Row Count' AS check_type,
    (SELECT COUNT(*) FROM comments) AS row_count;

-- 4. Verify Unique Constraint on Issues
SELECT
    repo_at,
    issue_id,
    COUNT(*) as duplicate_count
FROM issues
GROUP BY repo_at, issue_id
HAVING duplicate_count > 1;

-- 5. Verify Foreign Key Integrity for Comments
SELECT
    'Orphaned Comments' AS check_type,
    COUNT(*) AS orphaned_count
FROM comments c
LEFT JOIN issues i ON c.repo_at = i.repo_at AND c.issue_id = i.issue_id
WHERE i.id IS NULL;

-- 6. Check Foreign Key Constraint
PRAGMA foreign_key_list(comments);

-- 7. Sample Data Integrity Check
SELECT
    'Sample Issues' AS check_type,
    repo_at,
    issue_id,
    title,
    created
FROM issues
LIMIT 5;

-- 8. Sample Comments Data Integrity Check
SELECT
    'Sample Comments' AS check_type,
    repo_at,
    issue_id,
    comment_id,
    body,
    created
FROM comments
LIMIT 5;

-- 9. Verify Constraint on Comments (Issue ID and Comment ID Uniqueness)
SELECT
    issue_id,
    comment_id,
    COUNT(*) as duplicate_count
FROM comments
GROUP BY issue_id, comment_id
HAVING duplicate_count > 1;
