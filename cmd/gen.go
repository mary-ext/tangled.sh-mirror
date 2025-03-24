package main

import (
	cbg "github.com/whyrusleeping/cbor-gen"
	shtangled "tangled.sh/tangled.sh/core/api/tangled"
)

func main() {

	genCfg := cbg.Gen{
		MaxStringLength: 1_000_000,
	}

	if err := genCfg.WriteMapEncodersToFile(
		"api/tangled/cbor_gen.go",
		"tangled",
		shtangled.FeedStar{},
		shtangled.GraphFollow{},
		shtangled.KnotMember{},
		shtangled.PublicKey{},
		shtangled.RepoIssueComment{},
		shtangled.RepoIssueState{},
		shtangled.RepoIssue{},
		shtangled.Repo{},
		shtangled.RepoPull{},
		shtangled.RepoPullStatus{},
		shtangled.RepoPullComment{},
	); err != nil {
		panic(err)
	}

}
