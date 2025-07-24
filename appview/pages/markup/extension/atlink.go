// heavily inspired by: https://github.com/kaleocheng/goldmark-extensions

package extension

import (
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// An AtNode struct represents an AtNode
type AtNode struct {
	handle string
	ast.BaseInline
}

var _ ast.Node = &AtNode{}

// Dump implements Node.Dump.
func (n *AtNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// KindAt is a NodeKind of the At node.
var KindAt = ast.NewNodeKind("At")

// Kind implements Node.Kind.
func (n *AtNode) Kind() ast.NodeKind {
	return KindAt
}

var atRegexp = regexp.MustCompile(`(^|\s|\()(@)([a-zA-Z0-9.-]+)(\b)`)

type atParser struct{}

// NewAtParser return a new InlineParser that parses
// at expressions.
func NewAtParser() parser.InlineParser {
	return &atParser{}
}

func (s *atParser) Trigger() []byte {
	return []byte{'@'}
}

func (s *atParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, segment := block.PeekLine()
	m := atRegexp.FindSubmatchIndex(line)
	if m == nil {
		return nil
	}
	atSegment := text.NewSegment(segment.Start, segment.Start+m[1])
	block.Advance(m[1])
	node := &AtNode{}
	node.AppendChild(node, ast.NewTextSegment(atSegment))
	node.handle = string(atSegment.Value(block.Source())[1:])
	return node
}

// atHtmlRenderer is a renderer.NodeRenderer implementation that
// renders At nodes.
type atHtmlRenderer struct {
	html.Config
}

// NewAtHTMLRenderer returns a new AtHTMLRenderer.
func NewAtHTMLRenderer(opts ...html.Option) renderer.NodeRenderer {
	r := &atHtmlRenderer{
		Config: html.NewConfig(),
	}
	for _, opt := range opts {
		opt.SetHTMLOption(&r.Config)
	}
	return r
}

// RegisterFuncs implements renderer.NodeRenderer.RegisterFuncs.
func (r *atHtmlRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindAt, r.renderAt)
}

func (r *atHtmlRenderer) renderAt(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString(`<a href="/@`)
		w.WriteString(n.(*AtNode).handle)
		w.WriteString(`" class="mention">`)
	} else {
		w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

type atExt struct{}

// At is an extension that allow you to use at expression like '@user.bsky.social' .
var AtExt = &atExt{}

func (e *atExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(NewAtParser(), 500),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(NewAtHTMLRenderer(), 500),
	))
}
