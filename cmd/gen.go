package main

import (
	shbild "github.com/icyphox/bild/api/bild"
	cbg "github.com/whyrusleeping/cbor-gen"
)

func main() {

	genCfg := cbg.Gen{
		MaxStringLength: 1_000_000,
	}

	if err := genCfg.WriteMapEncodersToFile(
		"api/bild/cbor_gen.go",
		"bild",
		shbild.PublicKey{},
	); err != nil {
		panic(err)
	}

}
