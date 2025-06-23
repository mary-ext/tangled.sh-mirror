package rbac

import (
	"fmt"
	"slices"
	"strings"
)

func (e *Enforcer) getDomainsForUser(did string, keepFunc func(string) bool, stripFunc func(string) string) ([]string, error) {
	domains, err := e.E.GetDomainsForUser(did)
	if err != nil {
		return nil, err
	}

	n := 0
	for _, x := range domains {
		if keepFunc(x) {
			domains[n] = stripFunc(x)
			n++
		}
	}
	domains = domains[:n]

	return domains, nil
}

func (e *Enforcer) addOwner(domain, owner string) error {
	_, err := e.E.AddGroupingPolicy(owner, "server:owner", domain)
	return err
}

func (e *Enforcer) removeOwner(domain, owner string) error {
	_, err := e.E.RemoveGroupingPolicy(owner, "server:owner", domain)
	return err
}

func (e *Enforcer) addMember(domain, member string) error {
	_, err := e.E.AddGroupingPolicy(member, "server:member", domain)
	return err
}

func (e *Enforcer) removeMember(domain, member string) error {
	_, err := e.E.RemoveGroupingPolicy(member, "server:member", domain)
	return err
}

func (e *Enforcer) isRole(user, role, domain string) (bool, error) {
	roles, err := e.E.GetImplicitRolesForUser(user, domain)
	if err != nil {
		return false, err
	}
	if slices.Contains(roles, role) {
		return true, nil
	}
	return false, nil
}

func (e *Enforcer) isInviteAllowed(user, domain string) (bool, error) {
	return e.E.Enforce(user, domain, domain, "server:invite")
}

func checkRepoFormat(repo string) error {
	// sanity check, repo must be of the form ownerDid/repo
	if parts := strings.SplitN(repo, "/", 2); !strings.HasPrefix(parts[0], "did:") {
		return fmt.Errorf("invalid repo: %s", repo)
	}

	return nil
}

const spindlePrefix = "spindle:"

func intoSpindle(domain string) string {
	if !isSpindle(domain) {
		return spindlePrefix + domain
	}
	return domain
}

func unSpindle(domain string) string {
	if !isSpindle(domain) {
		return domain
	}
	return strings.TrimPrefix(domain, spindlePrefix)
}

func isSpindle(domain string) bool {
	return strings.HasPrefix(domain, spindlePrefix)
}

func isNotSpindle(domain string) bool {
	return !isSpindle(domain)
}
