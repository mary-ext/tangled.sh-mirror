package db

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

const TimeframeMonths = 7

func MakeProfileTimeline(e Execer, forDid string) (*models.ProfileTimeline, error) {
	timeline := models.ProfileTimeline{
		ByMonth: make([]models.ByMonth, TimeframeMonths),
	}
	currentMonth := time.Now().Month()
	timeframe := fmt.Sprintf("-%d months", TimeframeMonths)

	pulls, err := GetPullsByOwnerDid(e, forDid, timeframe)
	if err != nil {
		return nil, fmt.Errorf("error getting pulls by owner did: %w", err)
	}

	// group pulls by month
	for _, pull := range pulls {
		pullMonth := pull.Created.Month()

		if currentMonth-pullMonth >= TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - pullMonth
		items := &timeline.ByMonth[idx].PullEvents.Items

		*items = append(*items, &pull)
	}

	issues, err := GetIssues(
		e,
		FilterEq("did", forDid),
		FilterGte("created", time.Now().AddDate(0, -TimeframeMonths, 0)),
	)
	if err != nil {
		return nil, fmt.Errorf("error getting issues by owner did: %w", err)
	}

	for _, issue := range issues {
		issueMonth := issue.Created.Month()

		if currentMonth-issueMonth >= TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - issueMonth
		items := &timeline.ByMonth[idx].IssueEvents.Items

		*items = append(*items, &issue)
	}

	repos, err := GetRepos(e, 0, FilterEq("did", forDid))
	if err != nil {
		return nil, fmt.Errorf("error getting all repos by did: %w", err)
	}

	for _, repo := range repos {
		// TODO: get this in the original query; requires COALESCE because nullable
		var sourceRepo *models.Repo
		if repo.Source != "" {
			sourceRepo, err = GetRepoByAtUri(e, repo.Source)
			if err != nil {
				return nil, err
			}
		}

		repoMonth := repo.Created.Month()

		if currentMonth-repoMonth >= TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - repoMonth

		items := &timeline.ByMonth[idx].RepoEvents
		*items = append(*items, models.RepoEvent{
			Repo:   &repo,
			Source: sourceRepo,
		})
	}

	return &timeline, nil
}

func UpsertProfile(tx *sql.Tx, profile *models.Profile) error {
	defer tx.Rollback()

	// update links
	_, err := tx.Exec(`delete from profile_links where did = ?`, profile.Did)
	if err != nil {
		return err
	}
	// update vanity stats
	_, err = tx.Exec(`delete from profile_stats where did = ?`, profile.Did)
	if err != nil {
		return err
	}

	// update pinned repos
	_, err = tx.Exec(`delete from profile_pinned_repositories where did = ?`, profile.Did)
	if err != nil {
		return err
	}

	includeBskyValue := 0
	if profile.IncludeBluesky {
		includeBskyValue = 1
	}

	_, err = tx.Exec(
		`insert or replace into profile (
			did,
			description,
			include_bluesky,
			location,
			pronouns
		)
		values (?, ?, ?, ?, ?)`,
		profile.Did,
		profile.Description,
		includeBskyValue,
		profile.Location,
		profile.Pronouns,
	)

	if err != nil {
		log.Println("profile", "err", err)
		return err
	}

	for _, link := range profile.Links {
		if link == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_links (did, link) values (?, ?)`,
			profile.Did,
			link,
		)

		if err != nil {
			log.Println("profile_links", "err", err)
			return err
		}
	}

	for _, v := range profile.Stats {
		if v.Kind == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_stats (did, kind) values (?, ?)`,
			profile.Did,
			v.Kind,
		)

		if err != nil {
			log.Println("profile_stats", "err", err)
			return err
		}
	}

	for _, pin := range profile.PinnedRepos {
		if pin == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_pinned_repositories (did, at_uri) values (?, ?)`,
			profile.Did,
			pin,
		)

		if err != nil {
			log.Println("profile_pinned_repositories", "err", err)
			return err
		}
	}

	return tx.Commit()
}

func GetProfiles(e Execer, filters ...filter) (map[string]*models.Profile, error) {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	profilesQuery := fmt.Sprintf(
		`select
			id,
			did,
			description,
			include_bluesky,
			location,
			pronouns
		from
			profile
		%s`,
		whereClause,
	)
	rows, err := e.Query(profilesQuery, args...)
	if err != nil {
		return nil, err
	}

	profileMap := make(map[string]*models.Profile)
	for rows.Next() {
		var profile models.Profile
		var includeBluesky int
		var pronouns sql.Null[string]

		err = rows.Scan(&profile.ID, &profile.Did, &profile.Description, &includeBluesky, &profile.Location, &pronouns)
		if err != nil {
			return nil, err
		}

		if includeBluesky != 0 {
			profile.IncludeBluesky = true
		}

		if pronouns.Valid {
			profile.Pronouns = pronouns.V
		}

		profileMap[profile.Did] = &profile
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	// populate profile links
	inClause := strings.TrimSuffix(strings.Repeat("?, ", len(profileMap)), ", ")
	args = make([]any, len(profileMap))
	i := 0
	for did := range profileMap {
		args[i] = did
		i++
	}

	linksQuery := fmt.Sprintf("select link, did from profile_links where did in (%s)", inClause)
	rows, err = e.Query(linksQuery, args...)
	if err != nil {
		return nil, err
	}
	idxs := make(map[string]int)
	for did := range profileMap {
		idxs[did] = 0
	}
	for rows.Next() {
		var link, did string
		if err = rows.Scan(&link, &did); err != nil {
			return nil, err
		}

		idx := idxs[did]
		profileMap[did].Links[idx] = link
		idxs[did] = idx + 1
	}

	pinsQuery := fmt.Sprintf("select at_uri, did from profile_pinned_repositories where did in (%s)", inClause)
	rows, err = e.Query(pinsQuery, args...)
	if err != nil {
		return nil, err
	}
	idxs = make(map[string]int)
	for did := range profileMap {
		idxs[did] = 0
	}
	for rows.Next() {
		var link syntax.ATURI
		var did string
		if err = rows.Scan(&link, &did); err != nil {
			return nil, err
		}

		idx := idxs[did]
		profileMap[did].PinnedRepos[idx] = link
		idxs[did] = idx + 1
	}

	return profileMap, nil
}

func GetProfile(e Execer, did string) (*models.Profile, error) {
	var profile models.Profile
	var pronouns sql.Null[string]

	profile.Did = did

	includeBluesky := 0

	err := e.QueryRow(
		`select description, include_bluesky, location, pronouns from profile where did = ?`,
		did,
	).Scan(&profile.Description, &includeBluesky, &profile.Location, &pronouns)
	if err == sql.ErrNoRows {
		profile := models.Profile{}
		profile.Did = did
		return &profile, nil
	}

	if err != nil {
		return nil, err
	}

	if includeBluesky != 0 {
		profile.IncludeBluesky = true
	}

	if pronouns.Valid {
		profile.Pronouns = pronouns.V
	}

	rows, err := e.Query(`select link from profile_links where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		if err := rows.Scan(&profile.Links[i]); err != nil {
			return nil, err
		}
		i++
	}

	rows, err = e.Query(`select kind from profile_stats where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i = 0
	for rows.Next() {
		if err := rows.Scan(&profile.Stats[i].Kind); err != nil {
			return nil, err
		}
		value, err := GetVanityStat(e, profile.Did, profile.Stats[i].Kind)
		if err != nil {
			return nil, err
		}
		profile.Stats[i].Value = value
		i++
	}

	rows, err = e.Query(`select at_uri from profile_pinned_repositories where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i = 0
	for rows.Next() {
		if err := rows.Scan(&profile.PinnedRepos[i]); err != nil {
			return nil, err
		}
		i++
	}

	return &profile, nil
}

func GetVanityStat(e Execer, did string, stat models.VanityStatKind) (uint64, error) {
	query := ""
	var args []any
	switch stat {
	case models.VanityStatMergedPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, models.PullMerged)
	case models.VanityStatClosedPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, models.PullClosed)
	case models.VanityStatOpenPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, models.PullOpen)
	case models.VanityStatOpenIssueCount:
		query = `select count(id) from issues where did = ? and open = 1`
		args = append(args, did)
	case models.VanityStatClosedIssueCount:
		query = `select count(id) from issues where did = ? and open = 0`
		args = append(args, did)
	case models.VanityStatRepositoryCount:
		query = `select count(id) from repos where did = ?`
		args = append(args, did)
	}

	var result uint64
	err := e.QueryRow(query, args...).Scan(&result)
	if err != nil {
		return 0, err
	}

	return result, nil
}

func ValidateProfile(e Execer, profile *models.Profile) error {
	// ensure description is not too long
	if len(profile.Description) > 256 {
		return fmt.Errorf("Entered bio is too long.")
	}

	// ensure description is not too long
	if len(profile.Location) > 40 {
		return fmt.Errorf("Entered location is too long.")
	}

	// ensure pronouns are not too long
	if len(profile.Pronouns) > 40 {
		return fmt.Errorf("Entered pronouns are too long.")
	}

	// ensure links are in order
	err := validateLinks(profile)
	if err != nil {
		return err
	}

	// ensure all pinned repos are either own repos or collaborating repos
	repos, err := GetRepos(e, 0, FilterEq("did", profile.Did))
	if err != nil {
		log.Printf("getting repos for %s: %s", profile.Did, err)
	}

	collaboratingRepos, err := CollaboratingIn(e, profile.Did)
	if err != nil {
		log.Printf("getting collaborating repos for %s: %s", profile.Did, err)
	}

	var validRepos []syntax.ATURI
	for _, r := range repos {
		validRepos = append(validRepos, r.RepoAt())
	}
	for _, r := range collaboratingRepos {
		validRepos = append(validRepos, r.RepoAt())
	}

	for _, pinned := range profile.PinnedRepos {
		if pinned == "" {
			continue
		}
		if !slices.Contains(validRepos, pinned) {
			return fmt.Errorf("Invalid pinned repo: `%s, does not belong to own or collaborating repos", pinned)
		}
	}

	return nil
}

func validateLinks(profile *models.Profile) error {
	for i, link := range profile.Links {
		if link == "" {
			continue
		}

		parsedURL, err := url.Parse(link)
		if err != nil {
			return fmt.Errorf("Invalid URL '%s': %v\n", link, err)
		}

		if parsedURL.Scheme == "" {
			if strings.HasPrefix(link, "//") {
				profile.Links[i] = "https:" + link
			} else {
				profile.Links[i] = "https://" + link
			}
			continue
		} else if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("Warning: URL '%s' has unusual scheme: %s\n", link, parsedURL.Scheme)
		}

		// catch relative paths
		if parsedURL.Host == "" {
			return fmt.Errorf("Warning: URL '%s' appears to be a relative path\n", link)
		}
	}
	return nil
}
