package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/knotclient"
)

func main() {
	knots := flag.String("knots", "", "list of knots to connect to")
	retryFlag := flag.Duration("retry", 1*time.Minute, "retry interval")
	maxRetryFlag := flag.Duration("max-retry", 30*time.Minute, "max retry interval")
	workerCount := flag.Int("workers", 10, "goroutine pool size")

	flag.Parse()

	if *knots == "" {
		fmt.Println("error: -knots is required")
		flag.Usage()
		return
	}

	ccfg := knotclient.ConsumerConfig{
		ProcessFunc:      processEvent,
		RetryInterval:    *retryFlag,
		MaxRetryInterval: *maxRetryFlag,
		WorkerCount:      *workerCount,
		Dev:              true,
	}
	for k := range strings.SplitSeq(*knots, ",") {
		ccfg.AddEventSource(knotclient.NewEventSource(k))
	}

	consumer := knotclient.NewEventConsumer(ccfg)

	ctx, cancel := context.WithCancel(context.Background())
	consumer.Start(ctx)
	time.Sleep(1 * time.Hour)
	cancel()
	consumer.Stop()
}

func processEvent(_ context.Context, source knotclient.EventSource, msg knotclient.Message) error {
	fmt.Printf("From %s (%s, %s): %s\n", source.Knot, msg.Rkey, msg.Nsid, string(msg.EventJson))
	return nil
}
