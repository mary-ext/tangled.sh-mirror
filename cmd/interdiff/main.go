package main

import (
	"fmt"
	"os"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"tangled.sh/tangled.sh/core/interdiff"
)

func main() {
	patch1, err := os.Open("patches/g1.patch")
	if err != nil {
		fmt.Println(err)
	}
	patch2, err := os.Open("patches/g2.patch")
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

	interDiffResult := interdiff.Interdiff(files1, files2)
	fmt.Println(interDiffResult)
}
