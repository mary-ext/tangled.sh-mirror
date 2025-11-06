package models

import "fmt"

type RefKind int

const (
	RefKindIssue RefKind = iota
	RefKindPull
)

func (k RefKind) String() string {
	if k == RefKindIssue {
		return "issues"
	} else {
		return "pulls"
	}
}

// /@alice.com/cool-proj/issues/123
// /@alice.com/cool-proj/issues/123#comment-321
type ReferenceLink struct {
	Handle    string
	Repo      string
	Kind      RefKind
	SubjectId int
	CommentId *int
}

func (l ReferenceLink) String() string {
	comment := ""
	if l.CommentId != nil {
		comment = fmt.Sprintf("#comment-%d", *l.CommentId)
	}
	return fmt.Sprintf("/%s/%s/%s/%d%s",
		l.Handle,
		l.Repo,
		l.Kind.String(),
		l.SubjectId,
		comment,
	)
}

type RichReferenceLink struct {
	ReferenceLink
	Title string
	// reusing PullState for both issue & PR
	State PullState
}
