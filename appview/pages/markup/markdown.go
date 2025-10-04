// Package markup is an umbrella package for all markups and their renderers.
package markup

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	treeblood "github.com/wyatt915/goldmark-treeblood"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	callout "gitlab.com/staticnoise/goldmark-callout"
	htmlparse "golang.org/x/net/html"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/pages/repoinfo"
)

// RendererType defines the type of renderer to use based on context
type RendererType int

const (
	// RendererTypeRepoMarkdown is for repository documentation markdown files
	RendererTypeRepoMarkdown RendererType = iota
	// RendererTypeDefault is non-repo markdown, like issues/pulls/comments.
	RendererTypeDefault
)

// RenderContext holds the contextual data for rendering markdown.
// It can be initialized empty, and that'll skip any transformations.
type RenderContext struct {
	CamoUrl    string
	CamoSecret string
	repoinfo.RepoInfo
	IsDev        bool
	RendererType RendererType
	Sanitizer    Sanitizer
	Files        fs.FS
}

func (rctx *RenderContext) RenderMarkdown(source string) string {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(
					chromahtml.Standalone(false),
					chromahtml.WithClasses(true),
				),
				highlighting.WithCustomStyle(styles.Get("catppuccin-latte")),
			),
			extension.NewFootnote(
				extension.WithFootnoteIDPrefix([]byte("footnote")),
			),
			treeblood.MathML(),
			callout.CalloutExtention,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(html.WithUnsafe()),
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

	var processed strings.Builder
	if err := postProcess(rctx, strings.NewReader(buf.String()), &processed); err != nil {
		return source
	}

	return processed.String()
}

func postProcess(ctx *RenderContext, input io.Reader, output io.Writer) error {
	node, err := htmlparse.Parse(io.MultiReader(
		strings.NewReader("<html><body>"),
		input,
		strings.NewReader("</body></html>"),
	))
	if err != nil {
		return fmt.Errorf("failed to parse html: %w", err)
	}

	if node.Type == htmlparse.DocumentNode {
		node = node.FirstChild
	}

	visitNode(ctx, node)

	newNodes := make([]*htmlparse.Node, 0, 5)

	if node.Data == "html" {
		node = node.FirstChild
		for node != nil && node.Data != "body" {
			node = node.NextSibling
		}
	}
	if node != nil {
		if node.Data == "body" {
			child := node.FirstChild
			for child != nil {
				newNodes = append(newNodes, child)
				child = child.NextSibling
			}
		} else {
			newNodes = append(newNodes, node)
		}
	}

	for _, node := range newNodes {
		if err := htmlparse.Render(output, node); err != nil {
			return fmt.Errorf("failed to render processed html: %w", err)
		}
	}

	return nil
}

func visitNode(ctx *RenderContext, node *htmlparse.Node) {
	switch node.Type {
	case htmlparse.ElementNode:
		switch node.Data {
		case "img", "source":
			for i, attr := range node.Attr {
				if attr.Key != "src" {
					continue
				}

				camoUrl, _ := url.Parse(ctx.CamoUrl)
				dstUrl, _ := url.Parse(attr.Val)
				if dstUrl.Host != camoUrl.Host {
					attr.Val = ctx.imageFromKnotTransformer(attr.Val)
					attr.Val = ctx.camoImageLinkTransformer(attr.Val)
					node.Attr[i] = attr
				}
			}
		}

		for n := node.FirstChild; n != nil; n = n.NextSibling {
			visitNode(ctx, n)
		}
	default:
	}
}

func (rctx *RenderContext) SanitizeDefault(html string) string {
	return rctx.Sanitizer.SanitizeDefault(html)
}

func (rctx *RenderContext) SanitizeDescription(html string) string {
	return rctx.Sanitizer.SanitizeDescription(html)
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
			switch n := n.(type) {
			case *ast.Heading:
				a.rctx.anchorHeadingTransformer(n)
			case *ast.Link:
				a.rctx.relativeLinkTransformer(n)
			case *ast.Image:
				a.rctx.imageFromKnotAstTransformer(n)
				a.rctx.camoImageLinkAstTransformer(n)
			}
		case RendererTypeDefault:
			switch n := n.(type) {
			case *ast.Heading:
				a.rctx.anchorHeadingTransformer(n)
			case *ast.Image:
				a.rctx.imageFromKnotAstTransformer(n)
				a.rctx.camoImageLinkAstTransformer(n)
			}
		}

		return ast.WalkContinue, nil
	})
}

func (rctx *RenderContext) relativeLinkTransformer(link *ast.Link) {

	dst := string(link.Destination)

	if isAbsoluteUrl(dst) || isFragment(dst) || isMail(dst) {
		return
	}

	actualPath := rctx.actualPath(dst)

	newPath := path.Join("/", rctx.RepoInfo.FullName(), "tree", rctx.RepoInfo.Ref, actualPath)
	link.Destination = []byte(newPath)
}

func (rctx *RenderContext) imageFromKnotTransformer(dst string) string {
	if isAbsoluteUrl(dst) {
		return dst
	}

	scheme := "https"
	if rctx.IsDev {
		scheme = "http"
	}

	actualPath := rctx.actualPath(dst)

	repoName := fmt.Sprintf("%s/%s", rctx.RepoInfo.OwnerDid, rctx.RepoInfo.Name)

	query := fmt.Sprintf("repo=%s&ref=%s&path=%s&raw=true",
		url.PathEscape(repoName), url.PathEscape(rctx.RepoInfo.Ref), actualPath)

	parsedURL := &url.URL{
		Scheme:   scheme,
		Host:     rctx.Knot,
		Path:     path.Join("/xrpc", tangled.RepoBlobNSID),
		RawQuery: query,
	}
	newPath := parsedURL.String()
	return newPath
}

func (rctx *RenderContext) imageFromKnotAstTransformer(img *ast.Image) {
	dst := string(img.Destination)
	img.Destination = []byte(rctx.imageFromKnotTransformer(dst))
}

func (rctx *RenderContext) anchorHeadingTransformer(h *ast.Heading) {
	idGeneric, exists := h.AttributeString("id")
	if !exists {
		return // no id, nothing to do
	}
	id, ok := idGeneric.([]byte)
	if !ok {
		return
	}

	// create anchor link
	anchor := ast.NewLink()
	anchor.Destination = fmt.Appendf(nil, "#%s", string(id))
	anchor.SetAttribute([]byte("class"), []byte("anchor"))

	// create icon text
	iconText := ast.NewString([]byte("#"))
	anchor.AppendChild(anchor, iconText)

	// set class on heading
	h.SetAttribute([]byte("class"), []byte("heading"))

	// append anchor to heading
	h.AppendChild(h, anchor)
}

// actualPath decides when to join the file path with the
// current repository directory (essentially only when the link
// destination is relative. if it's absolute then we assume the
// user knows what they're doing.)
func (rctx *RenderContext) actualPath(dst string) string {
	if path.IsAbs(dst) {
		return dst
	}

	return path.Join(rctx.CurrentDir, dst)
}

func isAbsoluteUrl(link string) bool {
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	return parsed.IsAbs()
}

func isFragment(link string) bool {
	return strings.HasPrefix(link, "#")
}

func isMail(link string) bool {
	return strings.HasPrefix(link, "mailto:")
}
