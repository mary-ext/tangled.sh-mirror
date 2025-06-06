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
	sourcesFlag := flag.String("sources", "", "list of wss sources")
	retryFlag := flag.Duration("retry", 1*time.Minute, "retry interval")
	maxRetryFlag := flag.Duration("max-retry", 30*time.Minute, "max retry interval")
	workerCount := flag.Int("workers", 10, "goroutine pool size")

	flag.Parse()

	if *sourcesFlag == "" {
		fmt.Println("error: -sources is required")
		flag.Usage()
		return
	}

	sources := strings.Split(*sourcesFlag, ",")

	consumer := knotclient.NewEventConsumer(knotclient.ConsumerConfig{
		Sources:          sources,
		ProcessFunc:      processEvent,
		RetryInterval:    *retryFlag,
		MaxRetryInterval: *maxRetryFlag,
		WorkerCount:      *workerCount,
	})

	ctx, cancel := context.WithCancel(context.Background())
	consumer.Start(ctx)
	time.Sleep(1 * time.Hour)
	cancel()
	consumer.Stop()
}

func processEvent(source string, msg []byte) error {
	fmt.Printf("From %s: %s\n", source, string(msg))
	return nil
}
