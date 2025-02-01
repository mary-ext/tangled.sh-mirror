package state

import (
	"database/sql"
	"path"

	sqladapter "github.com/Blank-Xu/sql-adapter"
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
m = (r.act == p.act && r.dom == p.dom && keyMatch2(r.obj, p.obj) && g(r.sub, p.sub, r.dom))
`
)

type Enforcer struct {
	E      *casbin.SyncedEnforcer
	domain string
}

func keyMatch2(key1 string, key2 string) bool {
	matched, _ := path.Match(key2, key1)
	return matched
}

func NewEnforcer(domain string) (*Enforcer, error) {
	m, err := model.NewModelFromString(Model)
	if err != nil {
		return nil, err
	}

	// TODO: conf this
	db, err := sql.Open("sqlite3", "appview.db")
	if err != nil {
		return nil, err
	}

	a, err := sqladapter.NewAdapter(db, "sqlite3", "acl")
	if err != nil {
		return nil, err
	}

	e, err := casbin.NewSyncedEnforcer(m, a)
	if err != nil {
		return nil, err
	}

	e.EnableAutoSave(true)
	e.AddFunction("keyMatch2", keyMatch2Func)

	// Add policies with patterns
	_, err = e.AddPolicies([][]string{
		{"server:owner", domain, domain, "server:invite"},
		{"server:owner", domain, domain, "repo:create"},
		{"server:owner", domain, domain, "repo:delete"},  // priveledged operation, delete any repo in domain
		{"server:member", domain, domain, "repo:create"}, // priveledged operation, delete any repo in domain
	})
	if err != nil {
		return nil, err
	}

	return &Enforcer{e, domain}, nil
}

func (e *Enforcer) AddOwner(owner string) error {
	_, err := e.E.AddGroupingPolicy(owner, "server:owner", e.domain)
	return err
}

func (e *Enforcer) AddMember(member string) error {
	_, err := e.E.AddGroupingPolicy(member, "server:member", e.domain)
	return err
}

func (e *Enforcer) AddRepo(member, domain, repo string) error {
	_, err := e.E.AddPolicies([][]string{
		{member, e.domain, repo, "repo:push"},
		{member, e.domain, repo, "repo:owner"},
		{member, e.domain, repo, "repo:invite"},
		{member, e.domain, repo, "repo:delete"},
	})
	return err
}

// keyMatch2Func is a wrapper for keyMatch2 to make it compatible with Casbin
func keyMatch2Func(args ...interface{}) (interface{}, error) {
	name1 := args[0].(string)
	name2 := args[1].(string)

	return keyMatch2(name1, name2), nil
}
