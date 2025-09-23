package main

import (
	"context"
	"os"

	"tangled.org/core/log"
	"tangled.org/core/spindle"
	_ "tangled.org/core/tid"
)

func main() {
	ctx := log.NewContext(context.Background(), "spindle")
	err := spindle.Run(ctx)
	if err != nil {
		log.FromContext(ctx).Error("error running spindle", "error", err)
		os.Exit(-1)
	}
}
