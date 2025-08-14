package serververify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/rbac"
)

var (
	FetchError = errors.New("failed to fetch owner")
)

// fetchOwner fetches the owner DID from a server's /owner endpoint
func fetchOwner(ctx context.Context, domain string, dev bool) (string, error) {
	scheme := "https"
	if dev {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/owner", scheme, domain)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to fetch /owner")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)) // read atmost 1kb of data
	if err != nil {
		return "", fmt.Errorf("failed to read /owner response: %w", err)
	}

	did := strings.TrimSpace(string(body))
	if did == "" {
		return "", fmt.Errorf("empty DID in /owner response")
	}

	return did, nil
}

type OwnerMismatch struct {
	expected string
	observed string
}

func (e *OwnerMismatch) Error() string {
	return fmt.Sprintf("owner mismatch: %q != %q", e.expected, e.observed)
}

// RunVerification verifies that the server at the given domain has the expected owner
func RunVerification(ctx context.Context, domain, expectedOwner string, dev bool) error {
	observedOwner, err := fetchOwner(ctx, domain, dev)
	if err != nil {
		return fmt.Errorf("%w: %w", FetchError, err)
	}

	if observedOwner != expectedOwner {
		return &OwnerMismatch{
			expected: expectedOwner,
			observed: observedOwner,
		}
	}

	return nil
}

// MarkSpindleVerified marks a spindle as verified in the DB and adds the user as its owner
func MarkSpindleVerified(d *db.DB, e *rbac.Enforcer, instance, owner string) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to create txn: %w", err)
	}
	defer func() {
		tx.Rollback()
		e.E.LoadPolicy()
	}()

	// mark this spindle as verified in the db
	rowId, err := db.VerifySpindle(
		tx,
		db.FilterEq("owner", owner),
		db.FilterEq("instance", instance),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to write to DB: %w", err)
	}

	err = e.AddSpindleOwner(instance, owner)
	if err != nil {
		return 0, fmt.Errorf("failed to update ACL: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit txn: %w", err)
	}

	err = e.E.SavePolicy()
	if err != nil {
		return 0, fmt.Errorf("failed to update ACL: %w", err)
	}

	return rowId, nil
}

// MarkKnotVerified marks a knot as verified and sets up ownership/permissions
func MarkKnotVerified(d *db.DB, e *rbac.Enforcer, domain, owner string) error {
	tx, err := d.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to start tx: %w", err)
	}
	defer func() {
		tx.Rollback()
		e.E.LoadPolicy()
	}()

	// mark as registered
	err = db.Register(tx, domain)
	if err != nil {
		return fmt.Errorf("failed to register domain: %w", err)
	}

	// add basic acls for this domain
	err = e.AddKnot(domain)
	if err != nil {
		return fmt.Errorf("failed to add knot to enforcer: %w", err)
	}

	// add this did as owner of this domain
	err = e.AddKnotOwner(domain, owner)
	if err != nil {
		return fmt.Errorf("failed to add knot owner to enforcer: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	err = e.E.SavePolicy()
	if err != nil {
		return fmt.Errorf("failed to update ACLs: %w", err)
	}

	return nil
}
