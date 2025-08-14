package main

import (
	cbg "github.com/whyrusleeping/cbor-gen"
	"tangled.sh/tangled.sh/core/api/tangled"
)

func main() {

	genCfg := cbg.Gen{
		MaxStringLength: 1_000_000,
	}

	if err := genCfg.WriteMapEncodersToFile(
		"api/tangled/cbor_gen.go",
		"tangled",
		tangled.ActorProfile{},
		tangled.FeedReaction{},
		tangled.FeedStar{},
		tangled.GitRefUpdate_Meta_CommitCount_ByEmail_Elem{},
		tangled.GitRefUpdate_Meta_CommitCount{},
		tangled.GitRefUpdate_Meta_LangBreakdown{},
		tangled.GitRefUpdate_Meta{},
		tangled.GitRefUpdate_Pair{},
		tangled.GitRefUpdate{},
		tangled.GraphFollow{},
		tangled.KnotMember{},
		tangled.Knot{},
		tangled.PipelineStatus{},
		tangled.Pipeline_CloneOpts{},
		tangled.Pipeline_Dependency{},
		tangled.Pipeline_ManualTriggerData{},
		tangled.Pipeline_Pair{},
		tangled.Pipeline_PullRequestTriggerData{},
		tangled.Pipeline_PushTriggerData{},
		tangled.Pipeline_Step{},
		tangled.Pipeline_TriggerMetadata{},
		tangled.Pipeline_TriggerRepo{},
		tangled.Pipeline_Workflow{},
		tangled.Pipeline{},
		tangled.PublicKey{},
		tangled.RepoArtifact{},
		tangled.RepoCollaborator{},
		tangled.RepoIssueComment{},
		tangled.RepoIssueState{},
		tangled.RepoIssue{},
		tangled.RepoPullComment{},
		tangled.RepoPullStatus{},
		tangled.RepoPull_Source{},
		tangled.RepoPull{},
		tangled.Repo{},
		tangled.SpindleMember{},
		tangled.Spindle{},
		tangled.String{},
	); err != nil {
		panic(err)
	}

}
