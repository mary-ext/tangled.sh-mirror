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
		tangled.GitRefUpdate{},
		tangled.GitRefUpdate_CommitCountBreakdown{},
		tangled.GitRefUpdate_IndividualEmailCommitCount{},
		tangled.GitRefUpdate_LangBreakdown{},
		tangled.GitRefUpdate_IndividualLanguageSize{},
		tangled.GitRefUpdate_Meta{},
		tangled.GraphFollow{},
		tangled.Knot{},
		tangled.KnotMember{},
		tangled.Pipeline{},
		tangled.Pipeline_CloneOpts{},
		tangled.Pipeline_ManualTriggerData{},
		tangled.Pipeline_Pair{},
		tangled.Pipeline_PullRequestTriggerData{},
		tangled.Pipeline_PushTriggerData{},
		tangled.PipelineStatus{},
		tangled.Pipeline_TriggerMetadata{},
		tangled.Pipeline_TriggerRepo{},
		tangled.Pipeline_Workflow{},
		tangled.PublicKey{},
		tangled.Repo{},
		tangled.RepoArtifact{},
		tangled.RepoCollaborator{},
		tangled.RepoIssue{},
		tangled.RepoIssueComment{},
		tangled.RepoIssueState{},
		tangled.RepoPull{},
		tangled.RepoPullComment{},
		tangled.RepoPull_Source{},
		tangled.RepoPullStatus{},
		tangled.RepoPull_Target{},
		tangled.Spindle{},
		tangled.SpindleMember{},
		tangled.String{},
	); err != nil {
		panic(err)
	}

}
