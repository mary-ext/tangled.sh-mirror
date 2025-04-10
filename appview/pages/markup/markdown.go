// Package markup is an umbrella package for all markups and their renderers.
package markup

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

func RenderMarkdown(source string) string {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return source
	}
	return buf.String()
}

type RelativeLinkTransformer struct {
	User string
	Repo string
	Ref  string
}

func (t *RelativeLinkTransformer) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			fmt.Printf("Node: %T\n", n)
		}
		return ast.WalkContinue, nil
	})
}

func RenderMarkdownExtended(source, user, repo, ref string) string {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithParser(
			parser.NewParser(
				parser.WithASTTransformers(
					util.Prioritized(&RelativeLinkTransformer{
						User: user,
						Repo: repo,
						Ref:  ref,
					}, 999),
				),
			),
		),
	)

	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return source
	}
	return buf.String()
}
