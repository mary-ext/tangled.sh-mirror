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
	"tangled.sh/tangled.sh/core/knotclient/cursor"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/models"
	"tangled.sh/tangled.sh/core/spindle/queue"
)

const (
	rbacDomain = "thisserver"
)

type Spindle struct {
	jc  *jetstream.JetstreamClient
	db  *db.DB
	e   *rbac.Enforcer
	l   *slog.Logger
	n   *notifier.Notifier
	eng *engine.Engine
	jq  *queue.Queue
	cfg *config.Config
	ks  *knotclient.EventConsumer
}

func Run(ctx context.Context) error {
	logger := log.FromContext(ctx)

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
	e.E.EnableAutoSave(true)

	n := notifier.New()

	eng, err := engine.New(ctx, d, &n)
	if err != nil {
		return err
	}

	jq := queue.NewQueue(100, 2)

	collections := []string{
		tangled.SpindleMemberNSID,
		tangled.RepoNSID,
	}
	jc, err := jetstream.NewJetstreamClient(cfg.Server.JetstreamEndpoint, "spindle", collections, nil, logger, d, true, true)
	if err != nil {
		return fmt.Errorf("failed to setup jetstream client: %w", err)
	}
	jc.AddDid(cfg.Server.Owner)

	spindle := Spindle{
		jc:  jc,
		e:   e,
		db:  d,
		l:   logger,
		n:   &n,
		eng: eng,
		jq:  jq,
		cfg: cfg,
	}

	err = e.AddKnot(rbacDomain)
	if err != nil {
		return fmt.Errorf("failed to set rbac domain: %w", err)
	}
	err = spindle.configureOwner()
	if err != nil {
		return err
	}
	logger.Info("owner set", "did", cfg.Server.Owner)

	// starts a job queue runner in the background
	jq.Start()
	defer jq.Stop()

	cursorStore, err := cursor.NewSQLiteStore(cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to setup sqlite3 cursor store: %w", err)
	}

	err = jc.StartJetstream(ctx, spindle.ingest())
	if err != nil {
		return fmt.Errorf("failed to start jetstream consumer: %w", err)
	}

	// for each incoming sh.tangled.pipeline, we execute
	// spindle.processPipeline, which in turn enqueues the pipeline
	// job in the above registered queue.
	ccfg := knotclient.NewConsumerConfig()
	ccfg.Logger = logger
	ccfg.Dev = cfg.Server.Dev
	ccfg.ProcessFunc = spindle.processPipeline
	ccfg.CursorStore = cursorStore
	knownKnots, err := d.Knots()
	if err != nil {
		return err
	}
	for _, knot := range knownKnots {
		logger.Info("adding source start", "knot", knot)
		ccfg.Sources[knotclient.EventSource{knot}] = struct{}{}
	}
	spindle.ks = knotclient.NewEventConsumer(*ccfg)

	go func() {
		logger.Info("starting knot event consumer")
		spindle.ks.Start(ctx)
	}()

	logger.Info("starting spindle server", "address", cfg.Server.ListenAddr)
	logger.Error("server error", "error", http.ListenAndServe(cfg.Server.ListenAddr, spindle.Router()))

	return nil
}

func (s *Spindle) Router() http.Handler {
	mux := chi.NewRouter()

	mux.HandleFunc("/events", s.Events)
	mux.HandleFunc("/owner", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(s.cfg.Server.Owner))
	})
	mux.HandleFunc("/logs/{knot}/{rkey}/{name}", s.Logs)
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

		if pipeline.TriggerMetadata == nil {
			return fmt.Errorf("no trigger metadata found")
		}

		if pipeline.TriggerMetadata.Repo == nil {
			return fmt.Errorf("no repo data found")
		}

		// filter by repos
		_, err = s.db.GetRepo(
			pipeline.TriggerMetadata.Repo.Knot,
			pipeline.TriggerMetadata.Repo.Did,
			pipeline.TriggerMetadata.Repo.Repo,
		)
		if err != nil {
			return err
		}

		pipelineId := models.PipelineId{
			Knot: src.Knot,
			Rkey: msg.Rkey,
		}

		for _, w := range pipeline.Workflows {
			if w != nil {
				err := s.db.StatusPending(models.WorkflowId{
					PipelineId: pipelineId,
					Name:       w.Name,
				}, s.n)
				if err != nil {
					return err
				}
			}
		}

		ok := s.jq.Enqueue(queue.Job{
			Run: func() error {
				s.eng.StartWorkflows(ctx, &pipeline, pipelineId)
				return nil
			},
			OnFail: func(jobError error) {
				s.l.Error("pipeline run failed", "error", jobError)
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

func (s *Spindle) configureOwner() error {
	cfgOwner := s.cfg.Server.Owner
	serverOwner, err := s.e.GetUserByRole("server:owner", rbacDomain)
	if err != nil {
		return fmt.Errorf("failed to fetch server:owner: %w", err)
	}

	if len(serverOwner) == 0 {
		s.e.AddKnotOwner(rbacDomain, cfgOwner)
	} else {
		if serverOwner[0] != cfgOwner {
			return fmt.Errorf("server owner mismatch: %s != %s", cfgOwner, serverOwner[0])
		}
	}
	return nil
}
