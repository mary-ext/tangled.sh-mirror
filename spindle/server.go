package spindle

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"golang.org/x/net/context"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/knotserver/notifier"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/engine"
)

type Spindle struct {
	jc  *jetstream.JetstreamClient
	db  *db.DB
	e   *rbac.Enforcer
	l   *slog.Logger
	n   *notifier.Notifier
	eng *engine.Engine
}

func Run(ctx context.Context) error {
	cfg, err := config.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	d, err := db.Make(cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to setup db: %w", err)
	}

	e, err := rbac.NewEnforcer(cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to setup rbac enforcer: %w", err)
	}

	logger := log.FromContext(ctx)

	collections := []string{tangled.SpindleMemberNSID}
	jc, err := jetstream.NewJetstreamClient(cfg.Server.JetstreamEndpoint, "spindle", collections, nil, logger, d, true, false)
	if err != nil {
		return fmt.Errorf("failed to setup jetstream client: %w", err)
	}

	n := notifier.New()

	eng, err := engine.New(ctx, d)
	if err != nil {
		return err
	}

	spindle := Spindle{
		jc:  jc,
		e:   e,
		db:  d,
		l:   logger,
		n:   &n,
		eng: eng,
	}

	go func() {
		logger.Info("starting event consumer")
		knotEventSource := knotclient.NewEventSource("localhost:5555")
		ccfg := knotclient.ConsumerConfig{
			Logger:      logger,
			ProcessFunc: spindle.exec,
		}
		ccfg.AddEventSource(knotEventSource)

		ec := knotclient.NewEventConsumer(ccfg)

		ec.Start(ctx)
	}()

	logger.Info("starting spindle server", "address", cfg.Server.ListenAddr)
	logger.Error("server error", "error", http.ListenAndServe(cfg.Server.ListenAddr, spindle.Router()))

	return nil
}

func (s *Spindle) Router() http.Handler {
	mux := &http.ServeMux{}

	mux.HandleFunc("/events", s.Events)
	return mux
}

func (s *Spindle) exec(ctx context.Context, src knotclient.EventSource, msg knotclient.Message) error {
	pipeline := tangled.Pipeline{}
	err := json.Unmarshal(msg.EventJson, &pipeline)
	if err != nil {
		fmt.Println("error unmarshalling", err)
		return err
	}

	if msg.Nsid == tangled.PipelineNSID {
		err = s.eng.SetupPipeline(ctx, &pipeline, msg.Rkey)
		if err != nil {
			return err
		}
		err = s.eng.StartWorkflows(ctx, &pipeline, msg.Rkey)
		if err != nil {
			return err
		}
	}

	return nil
}
