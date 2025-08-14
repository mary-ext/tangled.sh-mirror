// Package markup is an umbrella package for all markups and their renderers.
package markup

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	htmlparse "golang.org/x/net/html"

	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
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
}

type Sanitizer struct {
	defaultPolicy *bluemonday.Policy
}

func (rctx *RenderContext) RenderMarkdown(source string) string {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
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
		if node.Data == "img" || node.Data == "source" {
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
	return rctx.Sanitizer.defaultPolicy.Sanitize(html)
}

func NewSanitizer() Sanitizer {
	return Sanitizer{
		defaultPolicy: defaultPolicy(),
	}
}
func defaultPolicy() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()

	// Allow generally safe attributes
	generalSafeAttrs := []string{
		"abbr", "accept", "accept-charset",
		"accesskey", "action", "align", "alt",
		"aria-describedby", "aria-hidden", "aria-label", "aria-labelledby",
		"axis", "border", "cellpadding", "cellspacing", "char",
		"charoff", "charset", "checked",
		"clear", "cols", "colspan", "color",
		"compact", "coords", "datetime", "dir",
		"disabled", "enctype", "for", "frame",
		"headers", "height", "hreflang",
		"hspace", "ismap", "label", "lang",
		"maxlength", "media", "method",
		"multiple", "name", "nohref", "noshade",
		"nowrap", "open", "prompt", "readonly", "rel", "rev",
		"rows", "rowspan", "rules", "scope",
		"selected", "shape", "size", "span",
		"start", "summary", "tabindex", "target",
		"title", "type", "usemap", "valign", "value",
		"vspace", "width", "itemprop",
	}

	generalSafeElements := []string{
		"h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "br", "b", "i", "strong", "em", "a", "pre", "code", "img", "tt",
		"div", "ins", "del", "sup", "sub", "p", "ol", "ul", "table", "thead", "tbody", "tfoot", "blockquote", "label",
		"dl", "dt", "dd", "kbd", "q", "samp", "var", "hr", "ruby", "rt", "rp", "li", "tr", "td", "th", "s", "strike", "summary",
		"details", "caption", "figure", "figcaption",
		"abbr", "bdo", "cite", "dfn", "mark", "small", "span", "time", "video", "wbr",
	}

	policy.AllowAttrs(generalSafeAttrs...).OnElements(generalSafeElements...)

	// video
	policy.AllowAttrs("src", "autoplay", "controls").OnElements("video")

	// checkboxes
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled", "data-source-position").OnElements("input")

	// centering content
	policy.AllowElements("center")

	policy.AllowAttrs("align", "style", "width", "height").Globally()
	policy.AllowStyles(
		"margin",
		"padding",
		"text-align",
		"font-weight",
		"text-decoration",
		"padding-left",
		"padding-right",
		"padding-top",
		"padding-bottom",
		"margin-left",
		"margin-right",
		"margin-top",
		"margin-bottom",
	)

	return policy
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
			case *ast.Link:
				a.rctx.relativeLinkTransformer(n)
			case *ast.Image:
				a.rctx.imageFromKnotAstTransformer(n)
				a.rctx.camoImageLinkAstTransformer(n)
			}
		case RendererTypeDefault:
			switch n := n.(type) {
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

	if isAbsoluteUrl(dst) {
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

	parsedURL := &url.URL{
		Scheme: scheme,
		Host:   rctx.Knot,
		Path: path.Join("/",
			rctx.RepoInfo.OwnerDid,
			rctx.RepoInfo.Name,
			"raw",
			url.PathEscape(rctx.RepoInfo.Ref),
			actualPath),
	}
	newPath := parsedURL.String()
	return newPath
}

func (rctx *RenderContext) imageFromKnotAstTransformer(img *ast.Image) {
	dst := string(img.Destination)
	img.Destination = []byte(rctx.imageFromKnotTransformer(dst))
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
