package models

type RefKind int

const (
	RefKindIssue RefKind = iota
	RefKindPull
)

// /@alice.com/cool-proj/issues/123
// /@alice.com/cool-proj/issues/123#comment-321
type ReferenceLink struct {
	Handle    string
	Repo      string
	Kind      RefKind
	SubjectId int
	CommentId *int
}
