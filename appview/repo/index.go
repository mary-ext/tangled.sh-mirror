package repo

import (
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"

	"tangled.sh/tangled.sh/core/appview/commitverify"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/types"

	"github.com/go-chi/chi/v5"
	"github.com/go-enry/go-enry/v2"
)

func (rp *Repo) RepoIndex(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		rp.pages.Error503(w)
		return
	}

	result, err := us.Index(f.OwnerDid(), f.Name, ref)
	if err != nil {
		rp.pages.Error503(w)
		log.Println("failed to reach knotserver", err)
		return
	}

	tagMap := make(map[string][]string)
	for _, tag := range result.Tags {
		hash := tag.Hash
		if tag.Tag != nil {
			hash = tag.Tag.Target.String()
		}
		tagMap[hash] = append(tagMap[hash], tag.Name)
	}

	for _, branch := range result.Branches {
		hash := branch.Hash
		tagMap[hash] = append(tagMap[hash], branch.Name)
	}

	sortFiles(result.Files)

	slices.SortFunc(result.Branches, func(a, b types.Branch) int {
		if a.Name == result.Ref {
			return -1
		}
		if a.IsDefault {
			return -1
		}
		if b.IsDefault {
			return 1
		}
		if a.Commit != nil && b.Commit != nil {
			if a.Commit.Committer.When.Before(b.Commit.Committer.When) {
				return 1
			} else {
				return -1
			}
		}
		return strings.Compare(a.Name, b.Name) * -1
	})

	commitCount := len(result.Commits)
	branchCount := len(result.Branches)
	tagCount := len(result.Tags)
	fileCount := len(result.Files)

	commitCount, branchCount, tagCount = balanceIndexItems(commitCount, branchCount, tagCount, fileCount)
	commitsTrunc := result.Commits[:min(commitCount, len(result.Commits))]
	tagsTrunc := result.Tags[:min(tagCount, len(result.Tags))]
	branchesTrunc := result.Branches[:min(branchCount, len(result.Branches))]

	emails := uniqueEmails(commitsTrunc)
	emailToDidMap, err := db.GetEmailToDid(rp.db, emails, true)
	if err != nil {
		log.Println("failed to get email to did map", err)
	}

	vc, err := commitverify.GetVerifiedObjectCommits(rp.db, emailToDidMap, commitsTrunc)
	if err != nil {
		log.Println(err)
	}

	user := rp.oauth.GetUser(r)
	repoInfo := f.RepoInfo(user)

	// TODO: a bit dirty
	languageInfo, err := rp.getLanguageInfo(f, us, result.Ref, ref == "")
	if err != nil {
		log.Printf("failed to compute language percentages: %s", err)
		// non-fatal
	}

	var shas []string
	for _, c := range commitsTrunc {
		shas = append(shas, c.Hash.String())
	}
	pipelines, err := getPipelineStatuses(rp.db, repoInfo, shas)
	if err != nil {
		log.Printf("failed to fetch pipeline statuses: %s", err)
		// non-fatal
	}

	rp.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser:      user,
		RepoInfo:          repoInfo,
		TagMap:            tagMap,
		RepoIndexResponse: *result,
		CommitsTrunc:      commitsTrunc,
		TagsTrunc:         tagsTrunc,
		// ForkInfo:           forkInfo, // TODO: reinstate this after xrpc properly lands
		BranchesTrunc:      branchesTrunc,
		EmailToDidOrHandle: emailToDidOrHandle(rp, emailToDidMap),
		VerifiedCommits:    vc,
		Languages:          languageInfo,
		Pipelines:          pipelines,
	})
}

func (rp *Repo) getLanguageInfo(
	f *reporesolver.ResolvedRepo,
	us *knotclient.UnsignedClient,
	currentRef string,
	isDefaultRef bool,
) ([]types.RepoLanguageDetails, error) {
	// first attempt to fetch from db
	langs, err := db.GetRepoLanguages(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("ref", currentRef),
	)

	if err != nil || langs == nil {
		// non-fatal, fetch langs from ks
		ls, err := us.RepoLanguages(f.OwnerDid(), f.Name, currentRef)
		if err != nil {
			return nil, err
		}
		if ls == nil {
			return nil, nil
		}

		for l, s := range ls.Languages {
			langs = append(langs, db.RepoLanguage{
				RepoAt:       f.RepoAt(),
				Ref:          currentRef,
				IsDefaultRef: isDefaultRef,
				Language:     l,
				Bytes:        s,
			})
		}

		// update appview's cache
		err = db.InsertRepoLanguages(rp.db, langs)
		if err != nil {
			// non-fatal
			log.Println("failed to cache lang results", err)
		}
	}

	var total int64
	for _, l := range langs {
		total += l.Bytes
	}

	var languageStats []types.RepoLanguageDetails
	for _, l := range langs {
		percentage := float32(l.Bytes) / float32(total) * 100
		color := enry.GetColor(l.Language)
		languageStats = append(languageStats, types.RepoLanguageDetails{
			Name:       l.Language,
			Percentage: percentage,
			Color:      color,
		})
	}

	sort.Slice(languageStats, func(i, j int) bool {
		if languageStats[i].Name == enry.OtherLanguage {
			return false
		}
		if languageStats[j].Name == enry.OtherLanguage {
			return true
		}
		if languageStats[i].Percentage != languageStats[j].Percentage {
			return languageStats[i].Percentage > languageStats[j].Percentage
		}
		return languageStats[i].Name < languageStats[j].Name
	})

	return languageStats, nil
}
