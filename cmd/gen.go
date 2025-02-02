package main

import (
	shtangled "github.com/icyphox/bild/api/tangled"
	cbg "github.com/whyrusleeping/cbor-gen"
)

func main() {

	genCfg := cbg.Gen{
		MaxStringLength: 1_000_000,
	}

	if err := genCfg.WriteMapEncodersToFile(
		"api/tangled/cbor_gen.go",
		"tangled",
		shtangled.PublicKey{},
	); err != nil {
		panic(err)
	}

}
