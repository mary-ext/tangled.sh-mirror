// Package markup is an umbrella package for all markups and their renderers.
package markup

import (
	"bytes"
	"net/url"
	"path"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
)

// RendererType defines the type of renderer to use based on context
type RendererType int

const (
	// RendererTypeRepoMarkdown is for repository documentation markdown files
	RendererTypeRepoMarkdown RendererType = iota
)

// RenderContext holds the contextual data for rendering markdown.
// It can be initialized empty, and that'll skip any transformations.
type RenderContext struct {
	repoinfo.RepoInfo
	IsDev        bool
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
			switch n.(type) {
			case *ast.Link:
				a.rctx.relativeLinkTransformer(n.(*ast.Link))
			case *ast.Image:
				a.rctx.imageFromKnotTransformer(n.(*ast.Image))
			}
			// more types here like RendererTypeIssue/Pull etc.
		}

		return ast.WalkContinue, nil
	})
}

func (rctx *RenderContext) relativeLinkTransformer(link *ast.Link) {
	dst := string(link.Destination)

	if isAbsoluteUrl(dst) {
		return
	}

	newPath := path.Join("/", rctx.RepoInfo.FullName(), "tree", rctx.RepoInfo.Ref, dst)
	link.Destination = []byte(newPath)
}

func (rctx *RenderContext) imageFromKnotTransformer(img *ast.Image) {
	dst := string(img.Destination)

	if isAbsoluteUrl(dst) {
		return
	}

	// strip leading './'
	if len(dst) >= 2 && dst[0:2] == "./" {
		dst = dst[2:]
	}

	scheme := "https"
	if rctx.IsDev {
		scheme = "http"
	}
	parsedURL := &url.URL{
		Scheme: scheme,
		Host:   rctx.Knot,
		Path: path.Join("/",
			rctx.RepoInfo.OwnerDid,
			rctx.RepoInfo.Name,
			"raw",
			url.PathEscape(rctx.RepoInfo.Ref),
			dst),
	}
	newPath := parsedURL.String()
	img.Destination = []byte(newPath)
}

func isAbsoluteUrl(link string) bool {
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	return parsed.IsAbs()
}
