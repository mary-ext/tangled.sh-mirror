package commitverify

import (
	"log"

	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/crypto"
	"tangled.org/core/types"
)

type verifiedCommit struct {
	fingerprint string
	hash        string
}

type VerifiedCommits map[verifiedCommit]struct{}

func (vcs VerifiedCommits) IsVerified(hash string) bool {
	for vc := range vcs {
		if vc.hash == hash {
			return true
		}
	}
	return false
}

func (vcs VerifiedCommits) Fingerprint(hash string) string {
	for vc := range vcs {
		if vc.hash == hash {
			return vc.fingerprint
		}
	}
	return ""
}

func GetVerifiedObjectCommits(e db.Execer, emailToDid map[string]string, commits []*object.Commit) (VerifiedCommits, error) {
	ndCommits := []types.NiceDiff{}
	for _, commit := range commits {
		ndCommits = append(ndCommits, ObjectCommitToNiceDiff(commit))
	}
	return GetVerifiedCommits(e, emailToDid, ndCommits)
}

func GetVerifiedCommits(e db.Execer, emailToDid map[string]string, ndCommits []types.NiceDiff) (VerifiedCommits, error) {
	vcs := VerifiedCommits{}

	didPubkeyCache := make(map[string][]models.PublicKey)

	for _, commit := range ndCommits {
		c := commit.Commit

		committerEmail := c.Committer.Email
		if did, exists := emailToDid[committerEmail]; exists {
			// check if we've already fetched public keys for this did
			pubKeys, ok := didPubkeyCache[did]
			if !ok {
				// fetch and cache public keys
				keys, err := db.GetPublicKeysForDid(e, did)
				if err != nil {
					log.Printf("failed to fetch pubkey for %s: %v", committerEmail, err)
					continue
				}
				pubKeys = keys
				didPubkeyCache[did] = pubKeys
			}

			// try to verify with any associated pubkeys
			for _, pk := range pubKeys {
				if _, ok := crypto.VerifyCommitSignature(pk.Key, commit); ok {

					fp, err := crypto.SSHFingerprint(pk.Key)
					if err != nil {
						log.Println("error computing ssh fingerprint:", err)
					}

					vc := verifiedCommit{fingerprint: fp, hash: c.This}
					vcs[vc] = struct{}{}
					break
				}
			}

		}
	}

	return vcs, nil
}

// ObjectCommitToNiceDiff is a compatibility function to convert a
// commit object into a NiceDiff structure.
func ObjectCommitToNiceDiff(c *object.Commit) types.NiceDiff {
	var niceDiff types.NiceDiff

	// set commit information
	niceDiff.Commit.Message = c.Message
	niceDiff.Commit.Author = c.Author
	niceDiff.Commit.This = c.Hash.String()
	niceDiff.Commit.Committer = c.Committer
	niceDiff.Commit.Tree = c.TreeHash.String()
	niceDiff.Commit.PGPSignature = c.PGPSignature

	changeId, ok := c.ExtraHeaders["change-id"]
	if ok {
		niceDiff.Commit.ChangedId = string(changeId)
	}

	// set parent hash if available
	if len(c.ParentHashes) > 0 {
		niceDiff.Commit.Parent = c.ParentHashes[0].String()
	}

	// XXX: Stats and Diff fields are typically populated
	// after fetching the actual diff information, which isn't
	// directly available in the commit object itself.

	return niceDiff
}
