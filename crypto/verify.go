package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/hiddeco/sshsig"
	"golang.org/x/crypto/ssh"
	"tangled.sh/tangled.sh/core/types"
)

func VerifySignature(pubKey, signature, payload []byte) (error, bool) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err), false
	}

	sig, err := sshsig.Unarmor(signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %w", err), false
	}

	buf := bytes.NewBuffer(payload)
	// we use sha-512 because ed25519 keys require it internally; rsa keys support
	// multiple algorithms but sha-512 is most secure, and git's ssh signing defaults
	// to sha-512 for all key types anyway.
	err = sshsig.Verify(buf, sig, pub, sshsig.HashSHA512, "git")
	return err, err == nil
}

// VerifyCommitSignature reconstructs the payload used to sign a commit. This is
// essentially the git cat-file output but without the gpgsig header.
//
// Caveats: signature verification will fail on commits with more than one parent,
// i.e. merge commits, because types.NiceDiff doesn't carry more than one Parent field
// and we are unable to reconstruct the payload correctly.
//
// Ideally this should directly operate on an *object.Commit.
func VerifyCommitSignature(pubKey string, commit types.NiceDiff) (error, bool) {
	signature := commit.Commit.PGPSignature

	author := bytes.NewBuffer([]byte{})
	committer := bytes.NewBuffer([]byte{})
	commit.Commit.Author.Encode(author)
	commit.Commit.Committer.Encode(committer)

	payload := strings.Builder{}

	fmt.Fprintf(&payload, "tree %s\n", commit.Commit.Tree)
	fmt.Fprintf(&payload, "parent %s\n", commit.Commit.Parent)
	fmt.Fprintf(&payload, "author %s\n", author.String())
	fmt.Fprintf(&payload, "committer %s\n", committer.String())
	if commit.Commit.ChangedId != "" {
		fmt.Fprintf(&payload, "change-id %s\n", commit.Commit.ChangedId)
	}
	fmt.Fprintf(&payload, "\n%s", commit.Commit.Message)

	return VerifySignature([]byte(pubKey), []byte(signature), []byte(payload.String()))
}

// SSHFingerprint computes the fingerprint of the supplied ssh pubkey.
func SSHFingerprint(pubKey string) (string, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKey))
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(pk.Marshal())
	return "SHA256:" + base64.StdEncoding.EncodeToString(hash[:]), nil
}
