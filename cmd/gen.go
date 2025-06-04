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
		tangled.FeedStar{},
		tangled.GitRefUpdate{},
		tangled.GraphFollow{},
		tangled.KnotMember{},
		tangled.Pipeline{},
		tangled.Pipeline_CloneOpts{},
		tangled.Pipeline_Workflow{},
		tangled.Pipeline_Workflow_Environment_Elem{},
		tangled.Pipeline_Dependencies_Elem{},
		tangled.Pipeline_ManualTriggerData{},
		tangled.Pipeline_ManualTriggerData_Inputs_Elem{},
		tangled.Pipeline_PullRequestTriggerData{},
		tangled.Pipeline_PushTriggerData{},
		tangled.Pipeline_Step{},
		tangled.Pipeline_TriggerMetadata{},
		tangled.Pipeline_TriggerRepo{},
		tangled.PublicKey{},
		tangled.Repo{},
		tangled.RepoArtifact{},
		tangled.RepoIssue{},
		tangled.RepoIssueComment{},
		tangled.RepoIssueState{},
		tangled.RepoPull{},
		tangled.RepoPullComment{},
		tangled.RepoPull_Source{},
		tangled.RepoPullStatus{},
	); err != nil {
		panic(err)
	}

}
