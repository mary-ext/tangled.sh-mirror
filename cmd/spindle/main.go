package main

import (
	"context"
	"os"

	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/spindle"
	_ "tangled.sh/tangled.sh/core/tid"
)

func main() {
	ctx := log.NewContext(context.Background(), "spindle")
	err := spindle.Run(ctx)
	if err != nil {
		log.FromContext(ctx).Error("error running spindle", "error", err)
		os.Exit(-1)
	}
}
