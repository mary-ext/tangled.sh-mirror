package rbac

import (
	"database/sql"
	"fmt"
	"strings"

	adapter "github.com/Blank-Xu/sql-adapter"
	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
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

	db, err := sql.Open("sqlite3", path)
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

func (e *Enforcer) AddDomain(domain string) error {
	// Add policies with patterns
	_, err := e.E.AddPolicies([][]string{
		{"server:owner", domain, domain, "server:invite"},
		{"server:member", domain, domain, "repo:create"},
	})
	if err != nil {
		return err
	}

	// all owners are also members
	_, err = e.E.AddGroupingPolicy("server:owner", "server:member", domain)
	return err
}

func (e *Enforcer) GetDomainsForUser(did string) ([]string, error) {
	return e.E.GetDomainsForUser(did)
}

func (e *Enforcer) AddOwner(domain, owner string) error {
	_, err := e.E.AddGroupingPolicy(owner, "server:owner", domain)
	return err
}

func (e *Enforcer) AddMember(domain, member string) error {
	_, err := e.E.AddGroupingPolicy(member, "server:member", domain)
	return err
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

	return membersWithoutRoles, nil
}

func (e *Enforcer) isRole(user, role, domain string) (bool, error) {
	return e.E.HasGroupingPolicy(user, role, domain)
}

func (e *Enforcer) IsServerOwner(user, domain string) (bool, error) {
	return e.isRole(user, "server:owner", domain)
}

func (e *Enforcer) IsServerMember(user, domain string) (bool, error) {
	return e.isRole(user, "server:member", domain)
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

func checkRepoFormat(repo string) error {
	// sanity check, repo must be of the form ownerDid/repo
	if parts := strings.SplitN(repo, "/", 2); !strings.HasPrefix(parts[0], "did:") {
		return fmt.Errorf("invalid repo: %s", repo)
	}

	return nil
}
