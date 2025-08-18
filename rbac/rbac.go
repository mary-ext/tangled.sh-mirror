package rbac

import (
	"database/sql"
	"slices"
	"strings"

	adapter "github.com/Blank-Xu/sql-adapter"
	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

const (
	ThisServer = "thisserver" // resource identifier for local rbac enforcement
)

const (
	Model = `
[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.act == p.act && r.dom == p.dom && r.obj == p.obj && g(r.sub, p.sub, r.dom)
`
)

type Enforcer struct {
	E *casbin.Enforcer
}

func NewEnforcer(path string) (*Enforcer, error) {
	m, err := model.NewModelFromString(Model)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", path+"?_foreign_keys=1")
	if err != nil {
		return nil, err
	}

	a, err := adapter.NewAdapter(db, "sqlite3", "acl")
	if err != nil {
		return nil, err
	}

	e, err := casbin.NewEnforcer(m, a)
	if err != nil {
		return nil, err
	}

	e.EnableAutoSave(false)

	return &Enforcer{e}, nil
}

func (e *Enforcer) AddKnot(knot string) error {
	// Add policies with patterns
	_, err := e.E.AddPolicies([][]string{
		{"server:owner", knot, knot, "server:invite"},
		{"server:member", knot, knot, "repo:create"},
	})
	if err != nil {
		return err
	}

	// all owners are also members
	_, err = e.E.AddGroupingPolicy("server:owner", "server:member", knot)
	return err
}

func (e *Enforcer) AddSpindle(spindle string) error {
	// the internal repr for spindles is spindle:foo.com
	spindle = intoSpindle(spindle)

	_, err := e.E.AddPolicies([][]string{
		{"server:owner", spindle, spindle, "server:invite"},
	})
	if err != nil {
		return err
	}

	// all owners are also members
	_, err = e.E.AddGroupingPolicy("server:owner", "server:member", spindle)
	return err
}

func (e *Enforcer) RemoveSpindle(spindle string) error {
	spindle = intoSpindle(spindle)
	_, err := e.E.DeleteDomains(spindle)
	return err
}

func (e *Enforcer) RemoveKnot(knot string) error {
	_, err := e.E.DeleteDomains(knot)
	return err
}

func (e *Enforcer) GetKnotsForUser(did string) ([]string, error) {
	keepFunc := isNotSpindle
	stripFunc := unSpindle
	return e.getDomainsForUser(did, keepFunc, stripFunc)
}

func (e *Enforcer) GetSpindlesForUser(did string) ([]string, error) {
	keepFunc := isSpindle
	stripFunc := unSpindle
	return e.getDomainsForUser(did, keepFunc, stripFunc)
}

func (e *Enforcer) AddKnotOwner(domain, owner string) error {
	return e.addOwner(domain, owner)
}

func (e *Enforcer) RemoveKnotOwner(domain, owner string) error {
	return e.removeOwner(domain, owner)
}

func (e *Enforcer) AddKnotMember(domain, member string) error {
	return e.addMember(domain, member)
}

func (e *Enforcer) RemoveKnotMember(domain, member string) error {
	return e.removeMember(domain, member)
}

func (e *Enforcer) AddSpindleOwner(domain, owner string) error {
	return e.addOwner(intoSpindle(domain), owner)
}

func (e *Enforcer) RemoveSpindleOwner(domain, owner string) error {
	return e.removeOwner(intoSpindle(domain), owner)
}

func (e *Enforcer) AddSpindleMember(domain, member string) error {
	return e.addMember(intoSpindle(domain), member)
}

func (e *Enforcer) RemoveSpindleMember(domain, member string) error {
	return e.removeMember(intoSpindle(domain), member)
}

func repoPolicies(member, domain, repo string) [][]string {
	return [][]string{
		{member, domain, repo, "repo:settings"},
		{member, domain, repo, "repo:push"},
		{member, domain, repo, "repo:owner"},
		{member, domain, repo, "repo:invite"},
		{member, domain, repo, "repo:delete"},
		{"server:owner", domain, repo, "repo:delete"}, // server owner can delete any repo
	}
}
func (e *Enforcer) AddRepo(member, domain, repo string) error {
	err := checkRepoFormat(repo)
	if err != nil {
		return err
	}

	_, err = e.E.AddPolicies(repoPolicies(member, domain, repo))
	return err
}
func (e *Enforcer) RemoveRepo(member, domain, repo string) error {
	err := checkRepoFormat(repo)
	if err != nil {
		return err
	}

	_, err = e.E.RemovePolicies(repoPolicies(member, domain, repo))
	return err
}

var (
	collaboratorPolicies = func(collaborator, domain, repo string) [][]string {
		return [][]string{
			{collaborator, domain, repo, "repo:collaborator"},
			{collaborator, domain, repo, "repo:settings"},
			{collaborator, domain, repo, "repo:push"},
		}
	}
)

func (e *Enforcer) AddCollaborator(collaborator, domain, repo string) error {
	err := checkRepoFormat(repo)
	if err != nil {
		return err
	}

	_, err = e.E.AddPolicies(collaboratorPolicies(collaborator, domain, repo))
	return err
}

func (e *Enforcer) RemoveCollaborator(collaborator, domain, repo string) error {
	err := checkRepoFormat(repo)
	if err != nil {
		return err
	}

	_, err = e.E.RemovePolicies(collaboratorPolicies(collaborator, domain, repo))
	return err
}

func (e *Enforcer) GetUserByRole(role, domain string) ([]string, error) {
	var membersWithoutRoles []string

	// this includes roles too, casbin does not differentiate.
	// the filtering criteria is to remove strings not starting with `did:`
	members, err := e.E.GetImplicitUsersForRole(role, domain)
	for _, m := range members {
		if strings.HasPrefix(m, "did:") {
			membersWithoutRoles = append(membersWithoutRoles, m)
		}
	}
	if err != nil {
		return nil, err
	}

	slices.Sort(membersWithoutRoles)
	return slices.Compact(membersWithoutRoles), nil
}

func (e *Enforcer) GetKnotUsersByRole(role, domain string) ([]string, error) {
	return e.GetUserByRole(role, domain)
}

func (e *Enforcer) GetSpindleUsersByRole(role, domain string) ([]string, error) {
	return e.GetUserByRole(role, intoSpindle(domain))
}

func (e *Enforcer) GetUserByRoleInRepo(role, domain, repo string) ([]string, error) {
	var users []string

	policies, err := e.E.GetImplicitUsersForResourceByDomain(repo, domain)
	for _, p := range policies {
		user := p[0]
		if strings.HasPrefix(user, "did:") {
			users = append(users, user)
		}
	}
	if err != nil {
		return nil, err
	}

	slices.Sort(users)
	return slices.Compact(users), nil
}

func (e *Enforcer) IsKnotOwner(user, domain string) (bool, error) {
	return e.isRole(user, "server:owner", domain)
}

func (e *Enforcer) IsKnotMember(user, domain string) (bool, error) {
	return e.isRole(user, "server:member", domain)
}

func (e *Enforcer) IsSpindleOwner(user, domain string) (bool, error) {
	return e.isRole(user, "server:owner", intoSpindle(domain))
}

func (e *Enforcer) IsSpindleMember(user, domain string) (bool, error) {
	return e.isRole(user, "server:member", intoSpindle(domain))
}

func (e *Enforcer) IsKnotInviteAllowed(user, domain string) (bool, error) {
	return e.isInviteAllowed(user, domain)
}

func (e *Enforcer) IsSpindleInviteAllowed(user, domain string) (bool, error) {
	return e.isInviteAllowed(user, intoSpindle(domain))
}

func (e *Enforcer) IsRepoCreateAllowed(user, domain string) (bool, error) {
	return e.E.Enforce(user, domain, domain, "repo:create")
}

func (e *Enforcer) IsRepoDeleteAllowed(user, domain string) (bool, error) {
	return e.E.Enforce(user, domain, domain, "repo:delete")
}

func (e *Enforcer) IsPushAllowed(user, domain, repo string) (bool, error) {
	return e.E.Enforce(user, domain, repo, "repo:push")
}

func (e *Enforcer) IsSettingsAllowed(user, domain, repo string) (bool, error) {
	return e.E.Enforce(user, domain, repo, "repo:settings")
}

func (e *Enforcer) IsCollaboratorInviteAllowed(user, domain, repo string) (bool, error) {
	return e.E.Enforce(user, domain, repo, "repo:invite")
}

// given a repo, what permissions does this user have? repo:owner? repo:invite? etc.
func (e *Enforcer) GetPermissionsInRepo(user, domain, repo string) []string {
	var permissions []string
	res := e.E.GetPermissionsForUserInDomain(user, domain)
	for _, p := range res {
		// get only permissions for this resource/repo
		if p[2] == repo {
			permissions = append(permissions, p[3])
		}
	}

	return permissions
}
