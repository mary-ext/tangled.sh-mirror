package models

import (
	"fmt"
	"regexp"

	"tangled.sh/tangled.sh/core/api/tangled"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

var (
	re = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
)

type PipelineId struct {
	Knot string
	Rkey string
}

func (p *PipelineId) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://did:web:%s/%s/%s", p.Knot, tangled.PipelineNSID, p.Rkey))
}

type WorkflowId struct {
	PipelineId
	Name string
}

func (wid WorkflowId) String() string {
	return fmt.Sprintf("%s-%s-%s", normalize(wid.Knot), wid.Rkey, normalize(wid.Name))
}

func normalize(name string) string {
	normalized := re.ReplaceAllString(name, "-")
	return normalized
}
