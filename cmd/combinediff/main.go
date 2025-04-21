package main

import (
	"fmt"
	"os"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"tangled.sh/tangled.sh/core/patchutil"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: combinediff <patch1> <patch2>")
		os.Exit(1)
	}

	patch1, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Println(err)
	}
	patch2, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Println(err)
	}

	files1, _, err := gitdiff.Parse(patch1)
	if err != nil {
		fmt.Println(err)
	}

	files2, _, err := gitdiff.Parse(patch2)
	if err != nil {
		fmt.Println(err)
	}

	combined := patchutil.CombineDiff(files1, files2)
	fmt.Println(combined)
}
