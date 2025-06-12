package spindle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/knotserver/notifier"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/queue"
)

type Spindle struct {
	jc  *jetstream.JetstreamClient
	db  *db.DB
	e   *rbac.Enforcer
	l   *slog.Logger
	n   *notifier.Notifier
	eng *engine.Engine
	jq  *queue.Queue
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
	eng, err := engine.New(ctx, d, &n)
	if err != nil {
		return err
	}

	jq := queue.NewQueue(100)

	// starts a job queue runner in the background
	jq.StartRunner()

	spindle := Spindle{
		jc:  jc,
		e:   e,
		db:  d,
		l:   logger,
		n:   &n,
		eng: eng,
		jq:  jq,
	}

	// for each incoming sh.tangled.pipeline, we execute
	// spindle.processPipeline, which in turn enqueues the pipeline
	// job in the above registered queue.
	go func() {
		logger.Info("starting event consumer")
		knotEventSource := knotclient.NewEventSource("localhost:5555")

		ccfg := knotclient.NewConsumerConfig()
		ccfg.Logger = logger
		ccfg.Dev = cfg.Server.Dev
		ccfg.ProcessFunc = spindle.processPipeline
		ccfg.AddEventSource(knotEventSource)

		ec := knotclient.NewEventConsumer(*ccfg)

		ec.Start(ctx)
	}()

	logger.Info("starting spindle server", "address", cfg.Server.ListenAddr)
	logger.Error("server error", "error", http.ListenAndServe(cfg.Server.ListenAddr, spindle.Router()))

	return nil
}

func (s *Spindle) Router() http.Handler {
	mux := chi.NewRouter()

	mux.HandleFunc("/events", s.Events)
	mux.HandleFunc("/logs/{pipelineID}", s.Logs)
	return mux
}

func (s *Spindle) processPipeline(ctx context.Context, src knotclient.EventSource, msg knotclient.Message) error {
	if msg.Nsid == tangled.PipelineNSID {
		pipeline := tangled.Pipeline{}
		err := json.Unmarshal(msg.EventJson, &pipeline)
		if err != nil {
			fmt.Println("error unmarshalling", err)
			return err
		}

		ok := s.jq.Enqueue(queue.Job{
			Run: func() error {
				// this is a "fake" at uri for now
				pipelineAtUri := fmt.Sprintf("at://%s/did:web:%s/%s", tangled.PipelineNSID, pipeline.TriggerMetadata.Repo.Knot, msg.Rkey)

				rkey := TID()
				err = s.eng.SetupPipeline(ctx, &pipeline, pipelineAtUri, rkey)
				if err != nil {
					return err
				}
				return s.eng.StartWorkflows(ctx, &pipeline, rkey)
			},
			OnFail: func(error) {
				s.l.Error("pipeline setup failed", "error", err)
			},
		})
		if ok {
			s.l.Info("pipeline enqueued successfully", "id", msg.Rkey)
		} else {
			s.l.Error("failed to enqueue pipeline: queue is full")
		}
	}

	return nil
}
