package markup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages/markup"
)

func TestMarkupParsing(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		wantHandles  []string
		wantRefLinks []models.ReferenceLink
	}{
		{
			name:        "normal link",
			source:      `[link](http://127.0.0.1:3000/alice.pds.tngl.boltless.dev/coolproj/issues/1)`,
			wantHandles: make([]string, 0),
			wantRefLinks: []models.ReferenceLink{
				{Handle: "alice.pds.tngl.boltless.dev", Repo: "coolproj", Kind: models.RefKindIssue, SubjectId: 1, CommentId: nil},
			},
		},
		{
			name:        "commonmark style autolink",
			source:      `<http://127.0.0.1:3000/alice.pds.tngl.boltless.dev/coolproj/issues/1>`,
			wantHandles: make([]string, 0),
			wantRefLinks: []models.ReferenceLink{
				{Handle: "alice.pds.tngl.boltless.dev", Repo: "coolproj", Kind: models.RefKindIssue, SubjectId: 1, CommentId: nil},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handles, refLinks := markup.FindReferences("http://127.0.0.1:3000", tt.source)
			assert.ElementsMatch(t, tt.wantHandles, handles)
			assert.ElementsMatch(t, tt.wantRefLinks, refLinks)
		})
	}
}
