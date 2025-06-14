package rbac_test

import (
	"database/sql"
	"testing"

	"tangled.sh/tangled.sh/core/rbac"

	adapter "github.com/Blank-Xu/sql-adapter"
	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func setup(t *testing.T) *rbac.Enforcer {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	a, err := adapter.NewAdapter(db, "sqlite3", "acl")
	assert.NoError(t, err)

	m, err := model.NewModelFromString(rbac.Model)
	assert.NoError(t, err)

	e, err := casbin.NewEnforcer(m, a)
	assert.NoError(t, err)

	e.EnableAutoSave(false)

	return &rbac.Enforcer{E: e}
}

func TestAddKnotAndRoles(t *testing.T) {
	e := setup(t)

	err := e.AddKnot("example.com")
	assert.NoError(t, err)

	err = e.AddKnotOwner("example.com", "did:plc:foo")
	assert.NoError(t, err)

	isOwner, err := e.IsKnotOwner("did:plc:foo", "example.com")
	assert.NoError(t, err)
	assert.True(t, isOwner)

	isMember, err := e.IsKnotMember("did:plc:foo", "example.com")
	assert.NoError(t, err)
	assert.True(t, isMember)
}

func TestAddMember(t *testing.T) {
	e := setup(t)

	err := e.AddKnot("example.com")
	assert.NoError(t, err)

	err = e.AddKnotOwner("example.com", "did:plc:foo")
	assert.NoError(t, err)

	err = e.AddKnotMember("example.com", "did:plc:bar")
	assert.NoError(t, err)

	isMember, err := e.IsKnotMember("did:plc:foo", "example.com")
	assert.NoError(t, err)
	assert.True(t, isMember)

	isMember, err = e.IsKnotMember("did:plc:bar", "example.com")
	assert.NoError(t, err)
	assert.True(t, isMember)

	isOwner, err := e.IsKnotOwner("did:plc:foo", "example.com")
	assert.NoError(t, err)
	assert.True(t, isOwner)

	// negated check here
	isOwner, err = e.IsKnotOwner("did:plc:bar", "example.com")
	assert.NoError(t, err)
	assert.False(t, isOwner)
}

func TestAddRepoPermissions(t *testing.T) {
	e := setup(t)

	knot := "example.com"

	fooUser := "did:plc:foo"
	fooRepo := "did:plc:foo/my-repo"

	barUser := "did:plc:bar"
	barRepo := "did:plc:bar/my-repo"

	_ = e.AddKnot(knot)
	_ = e.AddKnotMember(knot, fooUser)
	_ = e.AddKnotMember(knot, barUser)

	err := e.AddRepo(fooUser, knot, fooRepo)
	assert.NoError(t, err)

	err = e.AddRepo(barUser, knot, barRepo)
	assert.NoError(t, err)

	canPush, err := e.IsPushAllowed(fooUser, knot, fooRepo)
	assert.NoError(t, err)
	assert.True(t, canPush)

	canPush, err = e.IsPushAllowed(barUser, knot, barRepo)
	assert.NoError(t, err)
	assert.True(t, canPush)

	// negated
	canPush, err = e.IsPushAllowed(barUser, knot, fooRepo)
	assert.NoError(t, err)
	assert.False(t, canPush)

	canDelete, err := e.E.Enforce(fooUser, knot, fooRepo, "repo:delete")
	assert.NoError(t, err)
	assert.True(t, canDelete)

	// negated
	canDelete, err = e.E.Enforce(barUser, knot, fooRepo, "repo:delete")
	assert.NoError(t, err)
	assert.False(t, canDelete)
}

func TestCollaboratorPermissions(t *testing.T) {
	e := setup(t)

	knot := "example.com"
	repo := "did:plc:foo/my-repo"
	owner := "did:plc:foo"
	collaborator := "did:plc:bar"

	_ = e.AddKnot(knot)
	_ = e.AddRepo(owner, knot, repo)

	err := e.AddCollaborator(collaborator, knot, repo)
	assert.NoError(t, err)

	// all collaborator permissions granted
	perms := e.GetPermissionsInRepo(collaborator, knot, repo)
	assert.ElementsMatch(t, []string{
		"repo:settings", "repo:push", "repo:collaborator",
	}, perms)

	err = e.RemoveCollaborator(collaborator, knot, repo)
	assert.NoError(t, err)

	// all permissions removed
	perms = e.GetPermissionsInRepo(collaborator, knot, repo)
	assert.ElementsMatch(t, []string{}, perms)
}

func TestGetByRole(t *testing.T) {
	e := setup(t)

	knot := "example.com"
	repo := "did:plc:foo/my-repo"
	owner := "did:plc:foo"
	collaborator1 := "did:plc:bar"
	collaborator2 := "did:plc:baz"

	_ = e.AddKnot(knot)
	_ = e.AddRepo(owner, knot, repo)

	err := e.AddCollaborator(collaborator1, knot, repo)
	assert.NoError(t, err)

	err = e.AddCollaborator(collaborator2, knot, repo)
	assert.NoError(t, err)

	collaborators, err := e.GetUserByRoleInRepo("repo:collaborator", knot, repo)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"did:plc:foo", // owner
		"did:plc:bar", // collaborator1
		"did:plc:baz", // collaborator2
	}, collaborators)
}

func TestGetPermissionsInRepo(t *testing.T) {
	e := setup(t)

	user := "did:plc:foo"
	knot := "example.com"
	repo := "did:plc:foo/my-repo"

	_ = e.AddKnot(knot)
	_ = e.AddRepo(user, knot, repo)

	perms := e.GetPermissionsInRepo(user, knot, repo)
	assert.ElementsMatch(t, []string{
		"repo:settings", "repo:push", "repo:owner", "repo:invite", "repo:delete",
	}, perms)
}

func TestInvalidRepoFormat(t *testing.T) {
	e := setup(t)

	err := e.AddRepo("did:plc:foo", "example.com", "not-valid-format")
	assert.Error(t, err)
}

func TestGetKnotssForUser(t *testing.T) {
	e := setup(t)
	_ = e.AddKnot("example.com")
	_ = e.AddKnotOwner("example.com", "did:plc:foo")
	_ = e.AddKnotMember("example.com", "did:plc:bar")

	knots1, _ := e.GetKnotsForUser("did:plc:foo")
	assert.Contains(t, knots1, "example.com")

	knots2, _ := e.GetKnotsForUser("did:plc:bar")
	assert.Contains(t, knots2, "example.com")
}

func TestGetUserByRole(t *testing.T) {
	e := setup(t)
	_ = e.AddKnot("example.com")
	_ = e.AddKnotMember("example.com", "did:plc:foo")
	_ = e.AddKnotOwner("example.com", "did:plc:bar")

	members, _ := e.GetUserByRole("server:member", "example.com")
	assert.Contains(t, members, "did:plc:foo")
	assert.Contains(t, members, "did:plc:bar") // due to inheritance
}

func TestEmptyUserPermissions(t *testing.T) {
	e := setup(t)
	allowed, _ := e.IsPushAllowed("did:plc:nobody", "unknown.com", "did:plc:nobody/repo")
	assert.False(t, allowed)
}

func TestDuplicatePolicyAddition(t *testing.T) {
	e := setup(t)
	_ = e.AddKnot("example.com")
	_ = e.AddRepo("did:plc:foo", "example.com", "did:plc:foo/repo")

	// add again
	err := e.AddRepo("did:plc:foo", "example.com", "did:plc:foo/repo")
	assert.NoError(t, err) // should not fail, but won't duplicate
}

func TestRemoveRepo(t *testing.T) {
	e := setup(t)
	repo := "did:plc:foo/repo"
	_ = e.AddKnot("example.com")
	_ = e.AddRepo("did:plc:foo", "example.com", repo)

	allowed, _ := e.IsSettingsAllowed("did:plc:foo", "example.com", repo)
	assert.True(t, allowed)

	_ = e.RemoveRepo("did:plc:foo", "example.com", repo)

	allowed, _ = e.IsSettingsAllowed("did:plc:foo", "example.com", repo)
	assert.False(t, allowed)
}

func TestAddKnotAndSpindle(t *testing.T) {
	e := setup(t)

	err := e.AddKnot("k.com")
	assert.NoError(t, err)

	err = e.AddSpindle("s.com")
	assert.NoError(t, err)

	err = e.AddKnotOwner("k.com", "did:plc:foo")
	assert.NoError(t, err)

	err = e.AddSpindleOwner("s.com", "did:plc:foo")
	assert.NoError(t, err)

	knots, err := e.GetKnotsForUser("did:plc:foo")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"k.com",
	}, knots)

	spindles, err := e.GetSpindlesForUser("did:plc:foo")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"s.com",
	}, spindles)
}

func TestAddSpindleAndRoles(t *testing.T) {
	e := setup(t)

	err := e.AddSpindle("s.com")
	assert.NoError(t, err)

	err = e.AddSpindleOwner("s.com", "did:plc:foo")
	assert.NoError(t, err)

	ok, err := e.IsSpindleOwner("did:plc:foo", "s.com")
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = e.IsSpindleMember("did:plc:foo", "s.com")
	assert.NoError(t, err)
	assert.True(t, ok)
}
