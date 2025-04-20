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
	IsCopy        bool                   `json:"is_copy"`
	IsRename      bool                   `json:"is_rename"`
}

type DiffStat struct {
	Insertions int64
	Deletions  int64
}

func (d *Diff) Stats() DiffStat {
	var stats DiffStat
	for _, f := range d.TextFragments {
		stats.Insertions += f.LinesAdded
		stats.Deletions += f.LinesDeleted
	}
	return stats
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

type DiffTree struct {
	Rev1  string          `json:"rev1"`
	Rev2  string          `json:"rev2"`
	Patch string          `json:"patch"`
	Diff  []*gitdiff.File `json:"diff"`
}

func (d *NiceDiff) ChangedFiles() []string {
	files := make([]string, len(d.Diff))

	for i, f := range d.Diff {
		if f.IsDelete {
			files[i] = f.Name.Old
		} else {
			files[i] = f.Name.New
		}
	}

	return files
}
