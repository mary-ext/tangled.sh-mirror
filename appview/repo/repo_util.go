package repo

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"

	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/appview/db"
)

func uniqueEmails(commits []*object.Commit) []string {
	emails := make(map[string]struct{})
	for _, commit := range commits {
		if commit.Author.Email != "" {
			emails[commit.Author.Email] = struct{}{}
		}
		if commit.Committer.Email != "" {
			emails[commit.Committer.Email] = struct{}{}
		}
	}
	var uniqueEmails []string
	for email := range emails {
		uniqueEmails = append(uniqueEmails, email)
	}
	return uniqueEmails
}

func balanceIndexItems(commitCount, branchCount, tagCount, fileCount int) (commitsTrunc int, branchesTrunc int, tagsTrunc int) {
	if commitCount == 0 && tagCount == 0 && branchCount == 0 {
		return
	}

	// typically 1 item on right side = 2 files in height
	availableSpace := fileCount / 2

	// clamp tagcount
	if tagCount > 0 {
		tagsTrunc = 1
		availableSpace -= 1 // an extra subtracted for headers etc.
	}

	// clamp branchcount
	if branchCount > 0 {
		branchesTrunc = min(max(branchCount, 1), 2)
		availableSpace -= branchesTrunc // an extra subtracted for headers etc.
	}

	// show
	if commitCount > 0 {
		commitsTrunc = max(availableSpace, 3)
	}

	return
}

func EmailToDidOrHandle(r *Repo, emails []string) map[string]string {
	emailToDid, err := db.GetEmailToDid(r.db, emails, true) // only get verified emails for mapping
	if err != nil {
		log.Printf("error fetching dids for emails: %v", err)
		return nil
	}

	var dids []string
	for _, v := range emailToDid {
		dids = append(dids, v)
	}
	resolvedIdents := r.idResolver.ResolveIdents(context.Background(), dids)

	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIdents {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	// Create map of email to didOrHandle for commit display
	emailToDidOrHandle := make(map[string]string)
	for email, did := range emailToDid {
		if didOrHandle, ok := didHandleMap[did]; ok {
			emailToDidOrHandle[email] = didOrHandle
		}
	}

	return emailToDidOrHandle
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	result := make([]byte, n)

	for i := 0; i < n; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		result[i] = letters[n.Int64()]
	}

	return string(result)
}
