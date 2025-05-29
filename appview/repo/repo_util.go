package repo

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"

	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/crypto"
	"tangled.sh/tangled.sh/core/types"
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

// emailToDidOrHandle takes an emailToDidMap from db.GetEmailToDid
// and resolves all dids to handles and returns a new map[string]string
func emailToDidOrHandle(r *Repo, emailToDidMap map[string]string) map[string]string {
	if emailToDidMap == nil {
		return nil
	}

	var dids []string
	for _, v := range emailToDidMap {
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
	for email, did := range emailToDidMap {
		if didOrHandle, ok := didHandleMap[did]; ok {
			emailToDidOrHandle[email] = didOrHandle
		}
	}

	return emailToDidOrHandle
}

func verifiedObjectCommits(r *Repo, emailToDid map[string]string, commits []*object.Commit) (map[string]bool, error) {
	ndCommits := []types.NiceDiff{}
	for _, commit := range commits {
		ndCommits = append(ndCommits, types.ObjectCommitToNiceDiff(commit))
	}
	return verifiedCommits(r, emailToDid, ndCommits)
}

func verifiedCommits(r *Repo, emailToDid map[string]string, ndCommits []types.NiceDiff) (map[string]bool, error) {
	hashToVerified := make(map[string]bool)

	didPubkeyCache := make(map[string][]db.PublicKey)

	for _, commit := range ndCommits {
		c := commit.Commit

		committerEmail := c.Committer.Email
		if did, exists := emailToDid[committerEmail]; exists {
			// check if we've already fetched public keys for this did
			pubKeys, ok := didPubkeyCache[did]
			if !ok {
				// fetch and cache public keys
				keys, err := db.GetPublicKeysForDid(r.db, did)
				if err != nil {
					log.Printf("failed to fetch pubkey for %s: %v", committerEmail, err)
					continue
				}
				pubKeys = keys
				didPubkeyCache[did] = pubKeys
			}

			verified := false

			// try to verify with any associated pubkeys
			for _, pk := range pubKeys {
				if _, ok := crypto.VerifyCommitSignature(pk.Key, commit); ok {
					verified = true
					break
				}
			}

			hashToVerified[c.This] = verified
		}
	}

	return hashToVerified, nil
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
