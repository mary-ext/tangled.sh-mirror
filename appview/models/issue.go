package models

import (
	"fmt"
	"sort"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
)

type Issue struct {
	Id         int64
	Did        string
	Rkey       string
	RepoAt     syntax.ATURI
	IssueId    int
	Created    time.Time
	Edited     *time.Time
	Deleted    *time.Time
	Title      string
	Body       string
	Open       bool
	Mentions   []syntax.DID
	References []syntax.ATURI

	// optionally, populate this when querying for reverse mappings
	// like comment counts, parent repo etc.
	Comments []IssueComment
	Labels   LabelState
	Repo     *Repo
}

func (i *Issue) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", i.Did, tangled.RepoIssueNSID, i.Rkey))
}

func (i *Issue) AsRecord() tangled.RepoIssue {
	mentions := make([]string, len(i.Mentions))
	for i, did := range i.Mentions {
		mentions[i] = string(did)
	}
	references := make([]string, len(i.References))
	for i, uri := range i.References {
		references[i] = string(uri)
	}
	return tangled.RepoIssue{
		Repo:       i.RepoAt.String(),
		Title:      i.Title,
		Body:       &i.Body,
		Mentions:   mentions,
		References: references,
		CreatedAt:  i.Created.Format(time.RFC3339),
	}
}

func (i *Issue) State() string {
	if i.Open {
		return "open"
	}
	return "closed"
}

type CommentListItem struct {
	Self    *IssueComment
	Replies []*IssueComment
}

func (it *CommentListItem) Participants() []syntax.DID {
	participantSet := make(map[syntax.DID]struct{})
	participants := []syntax.DID{}

	addParticipant := func(did syntax.DID) {
		if _, exists := participantSet[did]; !exists {
			participantSet[did] = struct{}{}
			participants = append(participants, did)
		}
	}

	addParticipant(syntax.DID(it.Self.Did))

	for _, c := range it.Replies {
		addParticipant(syntax.DID(c.Did))
	}

	return participants
}

func (i *Issue) CommentList() []CommentListItem {
	// Create a map to quickly find comments by their aturi
	toplevel := make(map[string]*CommentListItem)
	var replies []*IssueComment

	// collect top level comments into the map
	for _, comment := range i.Comments {
		if comment.IsTopLevel() {
			toplevel[comment.AtUri().String()] = &CommentListItem{
				Self: &comment,
			}
		} else {
			replies = append(replies, &comment)
		}
	}

	for _, r := range replies {
		parentAt := *r.ReplyTo
		if parent, exists := toplevel[parentAt]; exists {
			parent.Replies = append(parent.Replies, r)
		}
	}

	var listing []CommentListItem
	for _, v := range toplevel {
		listing = append(listing, *v)
	}

	// sort everything
	sortFunc := func(a, b *IssueComment) bool {
		return a.Created.Before(b.Created)
	}
	sort.Slice(listing, func(i, j int) bool {
		return sortFunc(listing[i].Self, listing[j].Self)
	})
	for _, r := range listing {
		sort.Slice(r.Replies, func(i, j int) bool {
			return sortFunc(r.Replies[i], r.Replies[j])
		})
	}

	return listing
}

func (i *Issue) Participants() []string {
	participantSet := make(map[string]struct{})
	participants := []string{}

	addParticipant := func(did string) {
		if _, exists := participantSet[did]; !exists {
			participantSet[did] = struct{}{}
			participants = append(participants, did)
		}
	}

	addParticipant(i.Did)

	for _, c := range i.Comments {
		addParticipant(c.Did)
	}

	return participants
}

func IssueFromRecord(did, rkey string, record tangled.RepoIssue) Issue {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	body := ""
	if record.Body != nil {
		body = *record.Body
	}

	return Issue{
		RepoAt:  syntax.ATURI(record.Repo),
		Did:     did,
		Rkey:    rkey,
		Created: created,
		Title:   record.Title,
		Body:    body,
		Open:    true, // new issues are open by default
	}
}

type IssueComment struct {
	Id         int64
	Did        string
	Rkey       string
	IssueAt    string
	ReplyTo    *string
	Body       string
	Created    time.Time
	Edited     *time.Time
	Deleted    *time.Time
	Mentions   []syntax.DID
	References []syntax.ATURI
}

func (i *IssueComment) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", i.Did, tangled.RepoIssueCommentNSID, i.Rkey))
}

func (i *IssueComment) AsRecord() tangled.RepoIssueComment {
	mentions := make([]string, len(i.Mentions))
	for i, did := range i.Mentions {
		mentions[i] = string(did)
	}
	references := make([]string, len(i.References))
	for i, uri := range i.References {
		references[i] = string(uri)
	}
	return tangled.RepoIssueComment{
		Body:       i.Body,
		Issue:      i.IssueAt,
		CreatedAt:  i.Created.Format(time.RFC3339),
		ReplyTo:    i.ReplyTo,
		Mentions:   mentions,
		References: references,
	}
}

func (i *IssueComment) IsTopLevel() bool {
	return i.ReplyTo == nil
}

func (i *IssueComment) IsReply() bool {
	return i.ReplyTo != nil
}

func IssueCommentFromRecord(did, rkey string, record tangled.RepoIssueComment) (*IssueComment, error) {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	ownerDid := did

	if _, err = syntax.ParseATURI(record.Issue); err != nil {
		return nil, err
	}

	i := record
	mentions := make([]syntax.DID, len(record.Mentions))
	for i, did := range record.Mentions {
		mentions[i] = syntax.DID(did)
	}
	references := make([]syntax.ATURI, len(record.References))
	for i, uri := range i.References {
		references[i] = syntax.ATURI(uri)
	}

	comment := IssueComment{
		Did:        ownerDid,
		Rkey:       rkey,
		Body:       record.Body,
		IssueAt:    record.Issue,
		ReplyTo:    record.ReplyTo,
		Created:    created,
		Mentions:   mentions,
		References: references,
	}

	return &comment, nil
}
