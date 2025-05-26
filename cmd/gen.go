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
		tangled.FeedStar{},
		tangled.GraphFollow{},
		tangled.KnotMember{},
		tangled.PublicKey{},
		tangled.RepoIssueComment{},
		tangled.RepoIssueState{},
		tangled.RepoIssue{},
		tangled.Repo{},
		tangled.RepoPull{},
		tangled.RepoPull_Source{},
		tangled.RepoPullStatus{},
		tangled.RepoPullComment{},
		tangled.RepoArtifact{},
		tangled.ActorProfile{},
		tangled.Knot{},
		tangled.KnotAck{},
	); err != nil {
		panic(err)
	}

}
