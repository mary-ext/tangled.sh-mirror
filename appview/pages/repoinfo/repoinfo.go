package repoinfo

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/state/userutil"
)

func (r RepoInfo) OwnerWithAt() string {
	if r.OwnerHandle != "" {
		return fmt.Sprintf("@%s", r.OwnerHandle)
	} else {
		return r.OwnerDid
	}
}

func (r RepoInfo) FullName() string {
	return path.Join(r.OwnerWithAt(), r.Name)
}

func (r RepoInfo) OwnerWithoutAt() string {
	if strings.HasPrefix(r.OwnerWithAt(), "@") {
		return strings.TrimPrefix(r.OwnerWithAt(), "@")
	} else {
		return userutil.FlattenDid(r.OwnerDid)
	}
}

func (r RepoInfo) FullNameWithoutAt() string {
	return path.Join(r.OwnerWithoutAt(), r.Name)
}

func (r RepoInfo) GetTabs() [][]string {
	tabs := [][]string{
		{"overview", "/", "square-chart-gantt"},
		{"issues", "/issues", "circle-dot"},
		{"pulls", "/pulls", "git-pull-request"},
	}

	if r.Roles.SettingsAllowed() {
		tabs = append(tabs, []string{"settings", "/settings", "cog"})
	}

	return tabs
}

type RepoInfo struct {
	Name         string
	OwnerDid     string
	OwnerHandle  string
	Description  string
	Knot         string
	RepoAt       syntax.ATURI
	IsStarred    bool
	Stats        db.RepoStats
	Roles        RolesInRepo
	Source       *db.Repo
	SourceHandle string
	Ref          string
	DisableFork  bool
}

// each tab on a repo could have some metadata:
//
// issues -> number of open issues etc.
// settings -> a warning icon to setup branch protection? idk
//
// we gather these bits of info here, because go templates
// are difficult to program in
func (r RepoInfo) TabMetadata() map[string]any {
	meta := make(map[string]any)

	if r.Stats.PullCount.Open > 0 {
		meta["pulls"] = r.Stats.PullCount.Open
	}

	if r.Stats.IssueCount.Open > 0 {
		meta["issues"] = r.Stats.IssueCount.Open
	}

	// more stuff?

	return meta
}

type RolesInRepo struct {
	Roles []string
}

func (r RolesInRepo) SettingsAllowed() bool {
	return slices.Contains(r.Roles, "repo:settings")
}

func (r RolesInRepo) CollaboratorInviteAllowed() bool {
	return slices.Contains(r.Roles, "repo:invite")
}

func (r RolesInRepo) RepoDeleteAllowed() bool {
	return slices.Contains(r.Roles, "repo:delete")
}

func (r RolesInRepo) IsOwner() bool {
	return slices.Contains(r.Roles, "repo:owner")
}

func (r RolesInRepo) IsCollaborator() bool {
	return slices.Contains(r.Roles, "repo:collaborator")
}

func (r RolesInRepo) IsPushAllowed() bool {
	return slices.Contains(r.Roles, "repo:push")
}
