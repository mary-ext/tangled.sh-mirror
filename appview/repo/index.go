package repo

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"

	"tangled.sh/tangled.sh/core/appview/commitverify"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
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

	secret, err := db.GetRegistrationKey(rp.db, f.Knot)
	if err != nil {
		log.Printf("failed to get registration key for %s: %s", f.Knot, err)
		rp.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
	}

	signedClient, err := knotclient.NewSignedClient(f.Knot, secret, rp.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		return
	}

	var forkInfo *types.ForkInfo
	if user != nil && (repoInfo.Roles.IsOwner() || repoInfo.Roles.IsCollaborator()) {
		forkInfo, err = getForkInfo(r, repoInfo, rp, f, result.Ref, user, signedClient)
		if err != nil {
			log.Printf("Failed to fetch fork information: %v", err)
			return
		}
	}

	// TODO: a bit dirty
	languageInfo, err := rp.getLanguageInfo(f, signedClient, result.Ref, ref == "")
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
		LoggedInUser:       user,
		RepoInfo:           repoInfo,
		TagMap:             tagMap,
		RepoIndexResponse:  *result,
		CommitsTrunc:       commitsTrunc,
		TagsTrunc:          tagsTrunc,
		ForkInfo:           forkInfo,
		BranchesTrunc:      branchesTrunc,
		EmailToDidOrHandle: emailToDidOrHandle(rp, emailToDidMap),
		VerifiedCommits:    vc,
		Languages:          languageInfo,
		Pipelines:          pipelines,
	})
}

func (rp *Repo) getLanguageInfo(
	f *reporesolver.ResolvedRepo,
	signedClient *knotclient.SignedClient,
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
		ls, err := signedClient.RepoLanguages(f.OwnerDid(), f.Name, currentRef)
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

func getForkInfo(
	r *http.Request,
	repoInfo repoinfo.RepoInfo,
	rp *Repo,
	f *reporesolver.ResolvedRepo,
	currentRef string,
	user *oauth.User,
	signedClient *knotclient.SignedClient,
) (*types.ForkInfo, error) {
	if user == nil {
		return nil, nil
	}

	forkInfo := types.ForkInfo{
		IsFork: repoInfo.Source != nil,
		Status: types.UpToDate,
	}

	if !forkInfo.IsFork {
		forkInfo.IsFork = false
		return &forkInfo, nil
	}

	us, err := knotclient.NewUnsignedClient(repoInfo.Source.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", repoInfo.Source.Knot)
		return nil, err
	}

	result, err := us.Branches(repoInfo.Source.Did, repoInfo.Source.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return nil, err
	}

	if !slices.ContainsFunc(result.Branches, func(branch types.Branch) bool {
		return branch.Name == currentRef
	}) {
		forkInfo.Status = types.MissingBranch
		return &forkInfo, nil
	}

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoHiddenRefNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		log.Printf("failed to connect to knot server: %v", err)
		return nil, err
	}

	resp, err := tangled.RepoHiddenRef(
		r.Context(),
		client,
		&tangled.RepoHiddenRef_Input{
			ForkRef:   currentRef,
			RemoteRef: currentRef,
			Repo:      f.RepoAt().String(),
		},
	)
	if err != nil || !resp.Success {
		if err != nil {
			log.Printf("failed to update tracking branch: %s", err)
		} else {
			log.Printf("failed to update tracking branch: success=false")
		}
		return nil, fmt.Errorf("failed to update tracking branch")
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", currentRef, currentRef)

	var status types.AncestorCheckResponse
	forkSyncableResp, err := signedClient.RepoForkAheadBehind(user.Did, string(f.RepoAt()), repoInfo.Name, currentRef, hiddenRef)
	if err != nil {
		log.Printf("failed to check if fork is ahead/behind: %s", err)
		return nil, err
	}

	if err := json.NewDecoder(forkSyncableResp.Body).Decode(&status); err != nil {
		log.Printf("failed to decode fork status: %s", err)
		return nil, err
	}

	forkInfo.Status = status.Status
	return &forkInfo, nil
}
