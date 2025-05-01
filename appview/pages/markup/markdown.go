// Package markup is an umbrella package for all markups and their renderers.
package markup

import (
	"bytes"
	"path"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// RendererType defines the type of renderer to use based on context
type RendererType int

const (
	// RendererTypeRepoMarkdown is for repository documentation markdown files
	RendererTypeRepoMarkdown RendererType = iota
	// RendererTypeIssueComment is for issue comments
	RendererTypeIssueComment
	// RendererTypePullComment is for pull request comments
	RendererTypePullComment
	// RendererTypeDefault is the default renderer with minimal transformations
	RendererTypeDefault
)

// RenderContext holds the contextual data for rendering markdown.
// It can be initialized empty, and that'll skip any transformations
// and use the default renderer (RendererTypeDefault).
type RenderContext struct {
	Ref          string
	FullRepoName string
	RendererType RendererType
}

func (rctx *RenderContext) RenderMarkdown(source string) string {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)

	if rctx != nil {
		var transformers []util.PrioritizedValue

		transformers = append(transformers, util.Prioritized(&MarkdownTransformer{rctx: rctx}, 10000))

		md.Parser().AddOptions(
			parser.WithASTTransformers(transformers...),
		)
	}

	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return source
	}
	return buf.String()
}

type MarkdownTransformer struct {
	rctx *RenderContext
}

func (a *MarkdownTransformer) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch a.rctx.RendererType {
		case RendererTypeRepoMarkdown:
			a.rctx.relativeLinkTransformer(n.(*ast.Link))
		case RendererTypeDefault:
			a.rctx.relativeLinkTransformer(n.(*ast.Link))
			// more types here like RendererTypeIssue/Pull etc.
		}

		return ast.WalkContinue, nil
	})
}

func (rctx *RenderContext) relativeLinkTransformer(link *ast.Link) {
	dst := string(link.Destination)

	if len(dst) == 0 || dst[0] == '#' ||
		bytes.Contains(link.Destination, []byte("://")) ||
		bytes.HasPrefix(link.Destination, []byte("mailto:")) {
		return
	}

	newPath := path.Join("/", rctx.FullRepoName, "tree", rctx.Ref, dst)
	link.Destination = []byte(newPath)
}
