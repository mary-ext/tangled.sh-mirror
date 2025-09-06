package serververify

import (
	"context"
	"errors"
	"fmt"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/xrpcclient"
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

	host := fmt.Sprintf("%s://%s", scheme, domain)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	res, err := tangled.Owner(ctx, xrpcc)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		return "", xrpcerr
	}

	return res.Owner, nil
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
		return err
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
	err = db.MarkRegistered(
		tx,
		db.FilterEq("did", owner),
		db.FilterEq("domain", domain),
	)
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
