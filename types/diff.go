package types

import (
	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type TextFragment struct {
	Header string         `json:"comment"`
	Lines  []gitdiff.Line `json:"lines"`
}

type Diff struct {
	Name struct {
		Old string `json:"old"`
		New string `json:"new"`
	} `json:"name"`
	TextFragments []gitdiff.TextFragment `json:"text_fragments"`
	IsBinary      bool                   `json:"is_binary"`
	IsNew         bool                   `json:"is_new"`
	IsDelete      bool                   `json:"is_delete"`
}

// A nicer git diff representation.
type NiceDiff struct {
	Commit struct {
		Message string           `json:"message"`
		Author  object.Signature `json:"author"`
		This    string           `json:"this"`
		Parent  string           `json:"parent"`
	} `json:"commit"`
	Stat struct {
		FilesChanged int `json:"files_changed"`
		Insertions   int `json:"insertions"`
		Deletions    int `json:"deletions"`
	} `json:"stat"`
	Diff []Diff `json:"diff"`
}
