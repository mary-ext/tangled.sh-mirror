package models

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"
)

type PullState int

const (
	PullClosed PullState = iota
	PullOpen
	PullMerged
	PullDeleted
)

func (p PullState) String() string {
	switch p {
	case PullOpen:
		return "open"
	case PullMerged:
		return "merged"
	case PullClosed:
		return "closed"
	case PullDeleted:
		return "deleted"
	default:
		return "closed"
	}
}

func (p PullState) IsOpen() bool {
	return p == PullOpen
}
func (p PullState) IsMerged() bool {
	return p == PullMerged
}
func (p PullState) IsClosed() bool {
	return p == PullClosed
}
func (p PullState) IsDeleted() bool {
	return p == PullDeleted
}

type Pull struct {
	// ids
	ID     int
	PullId int

	// at ids
	RepoAt   syntax.ATURI
	OwnerDid string
	Rkey     string

	// content
	Title        string
	Body         string
	TargetBranch string
	State        PullState
	Submissions  []*PullSubmission

	// stacking
	StackId        string // nullable string
	ChangeId       string // nullable string
	ParentChangeId string // nullable string

	// meta
	Created    time.Time
	PullSource *PullSource

	// optionally, populate this when querying for reverse mappings
	Labels LabelState
	Repo   *Repo
}

func (p Pull) AsRecord() tangled.RepoPull {
	var source *tangled.RepoPull_Source
	if p.PullSource != nil {
		source = &tangled.RepoPull_Source{}
		source.Branch = p.PullSource.Branch
		source.Sha = p.LatestSha()
		if p.PullSource.RepoAt != nil {
			s := p.PullSource.RepoAt.String()
			source.Repo = &s
		}
	}

	record := tangled.RepoPull{
		Title:     p.Title,
		Body:      &p.Body,
		CreatedAt: p.Created.Format(time.RFC3339),
		Target: &tangled.RepoPull_Target{
			Repo:   p.RepoAt.String(),
			Branch: p.TargetBranch,
		},
		Patch:  p.LatestPatch(),
		Source: source,
	}
	return record
}

type PullSource struct {
	Branch string
	RepoAt *syntax.ATURI

	// optionally populate this for reverse mappings
	Repo *Repo
}

type PullSubmission struct {
	// ids
	ID int

	// at ids
	PullAt syntax.ATURI

	// content
	RoundNumber int
	Patch       string
	Combined    string
	Comments    []PullComment
	SourceRev   string // include the rev that was used to create this submission: only for branch/fork PRs

	// meta
	Created time.Time
}

type PullComment struct {
	// ids
	ID           int
	PullId       int
	SubmissionId int

	// at ids
	RepoAt    string
	OwnerDid  string
	CommentAt string

	// content
	Body string

	// meta
	Created time.Time
}

func (p *Pull) LastRoundNumber() int {
	return len(p.Submissions) - 1
}

func (p *Pull) LatestSubmission() *PullSubmission {
	return p.Submissions[p.LastRoundNumber()]
}

func (p *Pull) LatestPatch() string {
	return p.LatestSubmission().Patch
}

func (p *Pull) LatestSha() string {
	return p.LatestSubmission().SourceRev
}

func (p *Pull) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", p.OwnerDid, tangled.RepoPullNSID, p.Rkey))
}

func (p *Pull) IsPatchBased() bool {
	return p.PullSource == nil
}

func (p *Pull) IsBranchBased() bool {
	if p.PullSource != nil {
		if p.PullSource.RepoAt != nil {
			return p.PullSource.RepoAt == &p.RepoAt
		} else {
			// no repo specified
			return true
		}
	}
	return false
}

func (p *Pull) IsForkBased() bool {
	if p.PullSource != nil {
		if p.PullSource.RepoAt != nil {
			// make sure repos are different
			return p.PullSource.RepoAt != &p.RepoAt
		}
	}
	return false
}

func (p *Pull) IsStacked() bool {
	return p.StackId != ""
}

func (p *Pull) Participants() []string {
	participantSet := make(map[string]struct{})
	participants := []string{}

	addParticipant := func(did string) {
		if _, exists := participantSet[did]; !exists {
			participantSet[did] = struct{}{}
			participants = append(participants, did)
		}
	}

	addParticipant(p.OwnerDid)

	for _, s := range p.Submissions {
		for _, sp := range s.Participants() {
			addParticipant(sp)
		}
	}

	return participants
}

func (s PullSubmission) IsFormatPatch() bool {
	return patchutil.IsFormatPatch(s.Patch)
}

func (s PullSubmission) AsFormatPatch() []types.FormatPatch {
	patches, err := patchutil.ExtractPatches(s.Patch)
	if err != nil {
		log.Println("error extracting patches from submission:", err)
		return []types.FormatPatch{}
	}

	return patches
}

func (s *PullSubmission) Participants() []string {
	participantSet := make(map[string]struct{})
	participants := []string{}

	addParticipant := func(did string) {
		if _, exists := participantSet[did]; !exists {
			participantSet[did] = struct{}{}
			participants = append(participants, did)
		}
	}

	addParticipant(s.PullAt.Authority().String())

	for _, c := range s.Comments {
		addParticipant(c.OwnerDid)
	}

	return participants
}

func (s PullSubmission) CombinedPatch() string {
	if s.Combined == "" {
		return s.Patch
	}

	return s.Combined
}

type Stack []*Pull

// position of this pull in the stack
func (stack Stack) Position(pull *Pull) int {
	return slices.IndexFunc(stack, func(p *Pull) bool {
		return p.ChangeId == pull.ChangeId
	})
}

// all pulls below this pull (including self) in this stack
//
// nil if this pull does not belong to this stack
func (stack Stack) Below(pull *Pull) Stack {
	position := stack.Position(pull)

	if position < 0 {
		return nil
	}

	return stack[position:]
}

// all pulls below this pull (excluding self) in this stack
func (stack Stack) StrictlyBelow(pull *Pull) Stack {
	below := stack.Below(pull)

	if len(below) > 0 {
		return below[1:]
	}

	return nil
}

// all pulls above this pull (including self) in this stack
func (stack Stack) Above(pull *Pull) Stack {
	position := stack.Position(pull)

	if position < 0 {
		return nil
	}

	return stack[:position+1]
}

// all pulls below this pull (excluding self) in this stack
func (stack Stack) StrictlyAbove(pull *Pull) Stack {
	above := stack.Above(pull)

	if len(above) > 0 {
		return above[:len(above)-1]
	}

	return nil
}

// the combined format-patches of all the newest submissions in this stack
func (stack Stack) CombinedPatch() string {
	// go in reverse order because the bottom of the stack is the last element in the slice
	var combined strings.Builder
	for idx := range stack {
		pull := stack[len(stack)-1-idx]
		combined.WriteString(pull.LatestPatch())
		combined.WriteString("\n")
	}
	return combined.String()
}

// filter out PRs that are "active"
//
// PRs that are still open are active
func (stack Stack) Mergeable() Stack {
	var mergeable Stack

	for _, p := range stack {
		// stop at the first merged PR
		if p.State == PullMerged || p.State == PullClosed {
			break
		}

		// skip over deleted PRs
		if p.State != PullDeleted {
			mergeable = append(mergeable, p)
		}
	}

	return mergeable
}

type BranchDeleteStatus struct {
	Repo   *Repo
	Branch string
}
