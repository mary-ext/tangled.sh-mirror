package git

import (
	"context"
	"path"
	"strings"

	"github.com/go-enry/go-enry/v2"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type LangBreakdown map[string]int64

func (g *GitRepo) AnalyzeLanguages(ctx context.Context) (LangBreakdown, error) {
	sizes := make(map[string]int64)
	err := g.Walk(ctx, "", func(node object.TreeEntry, parent *object.Tree, root string) error {
		filepath := path.Join(root, node.Name)

		content, err := g.FileContentN(filepath, 16*1024) // 16KB
		if err != nil {
			return nil
		}

		if enry.IsGenerated(filepath, content) ||
			enry.IsBinary(content) ||
			strings.HasSuffix(filepath, "bun.lock") {
			return nil
		}

		language := analyzeLanguage(node, content)
		if group := enry.GetLanguageGroup(language); group != "" {
			language = group
		}

		langType := enry.GetLanguageType(language)
		if langType != enry.Programming && langType != enry.Markup && langType != enry.Unknown {
			return nil
		}

		sz, _ := parent.Size(node.Name)
		sizes[language] += sz

		return nil
	})

	if err != nil {
		return nil, err
	}

	return sizes, nil
}

func analyzeLanguage(node object.TreeEntry, content []byte) string {
	language, ok := enry.GetLanguageByExtension(node.Name)
	if ok {
		return language
	}

	language, ok = enry.GetLanguageByFilename(node.Name)
	if ok {
		return language
	}

	if len(content) == 0 {
		return enry.OtherLanguage
	}

	return enry.GetLanguage(node.Name, content)
}
