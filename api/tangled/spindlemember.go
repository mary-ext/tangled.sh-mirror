// Code generated by cmd/lexgen (see Makefile's lexgen); DO NOT EDIT.

package tangled

// schema: sh.tangled.spindle.member

import (
	"github.com/bluesky-social/indigo/lex/util"
)

const (
	SpindleMemberNSID = "sh.tangled.spindle.member"
)

func init() {
	util.RegisterType("sh.tangled.spindle.member", &SpindleMember{})
} //
// RECORDTYPE: SpindleMember
type SpindleMember struct {
	LexiconTypeID string `json:"$type,const=sh.tangled.spindle.member" cborgen:"$type,const=sh.tangled.spindle.member"`
	CreatedAt     string `json:"createdAt" cborgen:"createdAt"`
	// instance: spindle instance that the subject is now a member of
	Instance string `json:"instance" cborgen:"instance"`
	Subject  string `json:"subject" cborgen:"subject"`
}
