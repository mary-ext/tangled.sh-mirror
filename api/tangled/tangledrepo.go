// Code generated by cmd/lexgen (see Makefile's lexgen); DO NOT EDIT.

package tangled

// schema: sh.tangled.repo

import (
	"github.com/bluesky-social/indigo/lex/util"
)

const (
	RepoNSID = "sh.tangled.repo"
)

func init() {
	util.RegisterType("sh.tangled.repo", &Repo{})
} //
// RECORDTYPE: Repo
type Repo struct {
	LexiconTypeID string  `json:"$type,const=sh.tangled.repo" cborgen:"$type,const=sh.tangled.repo"`
	CreatedAt     string  `json:"createdAt" cborgen:"createdAt"`
	Description   *string `json:"description,omitempty" cborgen:"description,omitempty"`
	// knot: knot where the repo was created
	Knot string `json:"knot" cborgen:"knot"`
	// name: name of the repo
	Name  string `json:"name" cborgen:"name"`
	Owner string `json:"owner" cborgen:"owner"`
	// source: source of the repo
	Source *string `json:"source,omitempty" cborgen:"source,omitempty"`
	// spindle: CI runner to send jobs to and receive results from
	Spindle *string `json:"spindle,omitempty" cborgen:"spindle,omitempty"`
}
