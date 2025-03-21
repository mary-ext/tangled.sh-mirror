package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bluesky-social/jetstream/pkg/client"
	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/sotangled/tangled/jetstream"
)

// Simple in-memory implementation of DB interface
type MemoryDB struct {
	lastTimeUs int64
}

func (m *MemoryDB) GetLastTimeUs() (int64, error) {
	if m.lastTimeUs == 0 {
		return time.Now().UnixMicro(), nil
	}
	return m.lastTimeUs, nil
}

func (m *MemoryDB) SaveLastTimeUs(ts int64) error {
	m.lastTimeUs = ts
	return nil
}

func (m *MemoryDB) UpdateLastTimeUs(ts int64) error {
	m.lastTimeUs = ts
	return nil
}

func main() {
	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create in-memory DB
	db := &MemoryDB{}

	// Get query URL from flag
	var queryURL string
	flag.StringVar(&queryURL, "query-url", "", "Jetstream query URL containing DIDs")
	flag.Parse()

	if queryURL == "" {
		logger.Error("No query URL provided, use --query-url flag")
		os.Exit(1)
	}

	// Extract wantedDids parameters
	didParams := strings.Split(queryURL, "&wantedDids=")
	dids := make([]string, 0, len(didParams)-1)
	for i, param := range didParams {
		if i == 0 {
			// Skip the first part (the base URL with cursor)
			continue
		}
		dids = append(dids, param)
	}

	// Extract collections
	collections := []string{"sh.tangled.publicKey", "sh.tangled.knot.member"}

	// Create client configuration
	cfg := client.DefaultClientConfig()
	cfg.WebsocketURL = "wss://jetstream2.us-west.bsky.network/subscribe"
	cfg.WantedCollections = collections

	// Create jetstream client
	jsClient, err := jetstream.NewJetstreamClient(
		cfg.WebsocketURL,
		"tangled-jetstream",
		collections,
		cfg,
		logger,
		db,
		false,
	)
	if err != nil {
		logger.Error("Failed to create jetstream client", "error", err)
		os.Exit(1)
	}

	// Update DIDs
	jsClient.UpdateDids(dids)

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling with a buffered channel
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Process function for events
	processFunc := func(ctx context.Context, event *models.Event) error {
		// Log the event details
		logger.Info("Received event",
			"collection", event.Commit.Collection,
			"did", event.Did,
			"rkey", event.Commit.RKey,
			"action", event.Kind,
			"time_us", event.TimeUS,
		)

		// Save the last time_us
		if err := db.UpdateLastTimeUs(event.TimeUS); err != nil {
			logger.Error("Failed to update last time_us", "error", err)
		}

		return nil
	}

	// Start jetstream
	if err := jsClient.StartJetstream(ctx, processFunc); err != nil {
		logger.Error("Failed to start jetstream", "error", err)
		os.Exit(1)
	}

	// Wait for signal instead of context.Done()
	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	cancel() // Cancel context after receiving signal

	// Shutdown gracefully with a timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		jsClient.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("Jetstream client shut down gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("Shutdown timed out, forcing exit")
	}
}
