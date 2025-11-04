package markup

import (
	"maps"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"tangled.org/core/appview/models"
	textension "tangled.org/core/appview/pages/markup/extension"
)

// FindReferences collects all links referencing tangled-related objects
// like issues, PRs, comments or even @-mentions
// This funciton doesn't actually check for the existence of records in the DB
// or the PDS; it merely returns a list of what are presumed to be references.
func FindReferences(baseUrl string, source string) ([]string, []models.ReferenceLink) {
	var (
		refLinkSet  = make(map[models.ReferenceLink]struct{})
		mentionsSet = make(map[string]struct{})
		md          = NewMarkdown()
		sourceBytes = []byte(source)
		root        = md.Parser().Parse(text.NewReader(sourceBytes))
	)
	// trim url scheme. the SSL shouldn't matter
	baseUrl = strings.TrimPrefix(baseUrl, "https://")
	baseUrl = strings.TrimPrefix(baseUrl, "http://")

	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case textension.KindAt:
			handle := n.(*textension.AtNode).Handle
			mentionsSet[handle] = struct{}{}
			return ast.WalkSkipChildren, nil
		case ast.KindLink:
			dest := string(n.(*ast.Link).Destination)
			ref := parseTangledLink(baseUrl, dest)
			if ref != nil {
				refLinkSet[*ref] = struct{}{}
			}
			return ast.WalkSkipChildren, nil
		case ast.KindAutoLink:
			an := n.(*ast.AutoLink)
			if an.AutoLinkType == ast.AutoLinkURL {
				dest := string(an.URL(sourceBytes))
				ref := parseTangledLink(baseUrl, dest)
				if ref != nil {
					refLinkSet[*ref] = struct{}{}
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	mentions := slices.Collect(maps.Keys(mentionsSet))
	references := slices.Collect(maps.Keys(refLinkSet))
	return mentions, references
}

func parseTangledLink(baseHost string, urlStr string) *models.ReferenceLink {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	if u.Host != "" && !strings.EqualFold(u.Host, baseHost) {
		return nil
	}

	p := path.Clean(u.Path)
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	if len(parts) < 4 {
		// need at least: handle / repo / kind / id
		return nil
	}

	var (
		handle     = parts[0]
		repo       = parts[1]
		kindSeg    = parts[2]
		subjectSeg = parts[3]
	)

	handle = strings.TrimPrefix(handle, "@")

	var kind models.RefKind
	switch kindSeg {
	case "issues":
		kind = models.RefKindIssue
	case "pulls":
		kind = models.RefKindPull
	default:
		return nil
	}

	subjectId, err := strconv.Atoi(subjectSeg)
	if err != nil {
		return nil
	}
	var commentId *int
	if u.Fragment != "" {
		if strings.HasPrefix(u.Fragment, "comment-") {
			commentIdStr := u.Fragment[len("comment-"):]
			if id, err := strconv.Atoi(commentIdStr); err == nil {
				commentId = &id
			}
		}
	}

	return &models.ReferenceLink{
		Handle:    handle,
		Repo:      repo,
		Kind:      kind,
		SubjectId: subjectId,
		CommentId: commentId,
	}
}
