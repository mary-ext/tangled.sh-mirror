package spindle

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/eventconsumer/cursor"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/engines/nixery"
	"tangled.sh/tangled.sh/core/spindle/models"
	"tangled.sh/tangled.sh/core/spindle/queue"
	"tangled.sh/tangled.sh/core/spindle/secrets"
	"tangled.sh/tangled.sh/core/spindle/xrpc"
	"tangled.sh/tangled.sh/core/xrpc/serviceauth"
)

//go:embed motd
var motd []byte

const (
	rbacDomain = "thisserver"
)

type Spindle struct {
	jc    *jetstream.JetstreamClient
	db    *db.DB
	e     *rbac.Enforcer
	l     *slog.Logger
	n     *notifier.Notifier
	engs  map[string]models.Engine
	jq    *queue.Queue
	cfg   *config.Config
	ks    *eventconsumer.Consumer
	res   *idresolver.Resolver
	vault secrets.Manager
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

	var vault secrets.Manager
	switch cfg.Server.Secrets.Provider {
	case "openbao":
		if cfg.Server.Secrets.OpenBao.ProxyAddr == "" {
			return fmt.Errorf("openbao proxy address is required when using openbao secrets provider")
		}
		vault, err = secrets.NewOpenBaoManager(
			cfg.Server.Secrets.OpenBao.ProxyAddr,
			logger,
			secrets.WithMountPath(cfg.Server.Secrets.OpenBao.Mount),
		)
		if err != nil {
			return fmt.Errorf("failed to setup openbao secrets provider: %w", err)
		}
		logger.Info("using openbao secrets provider", "proxy_address", cfg.Server.Secrets.OpenBao.ProxyAddr, "mount", cfg.Server.Secrets.OpenBao.Mount)
	case "sqlite", "":
		vault, err = secrets.NewSQLiteManager(cfg.Server.DBPath, secrets.WithTableName("secrets"))
		if err != nil {
			return fmt.Errorf("failed to setup sqlite secrets provider: %w", err)
		}
		logger.Info("using sqlite secrets provider", "path", cfg.Server.DBPath)
	default:
		return fmt.Errorf("unknown secrets provider: %s", cfg.Server.Secrets.Provider)
	}

	nixeryEng, err := nixery.New(ctx, cfg)
	if err != nil {
		return err
	}

	jq := queue.NewQueue(100, 5)

	collections := []string{
		tangled.SpindleMemberNSID,
		tangled.RepoNSID,
		tangled.RepoCollaboratorNSID,
	}
	jc, err := jetstream.NewJetstreamClient(cfg.Server.JetstreamEndpoint, "spindle", collections, nil, logger, d, true, true)
	if err != nil {
		return fmt.Errorf("failed to setup jetstream client: %w", err)
	}
	jc.AddDid(cfg.Server.Owner)

	// Check if the spindle knows about any Dids;
	dids, err := d.GetAllDids()
	if err != nil {
		return fmt.Errorf("failed to get all dids: %w", err)
	}
	for _, d := range dids {
		jc.AddDid(d)
	}

	resolver := idresolver.DefaultResolver()

	spindle := Spindle{
		jc:    jc,
		e:     e,
		db:    d,
		l:     logger,
		n:     &n,
		engs:  map[string]models.Engine{"nixery": nixeryEng},
		jq:    jq,
		cfg:   cfg,
		res:   resolver,
		vault: vault,
	}

	err = e.AddSpindle(rbacDomain)
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

	// Stop vault token renewal if it implements Stopper
	if stopper, ok := vault.(secrets.Stopper); ok {
		defer stopper.Stop()
	}

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
	ccfg := eventconsumer.NewConsumerConfig()
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
		ccfg.Sources[eventconsumer.NewKnotSource(knot)] = struct{}{}
	}
	spindle.ks = eventconsumer.NewConsumer(*ccfg)

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

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(motd)
	})
	mux.HandleFunc("/events", s.Events)
	mux.HandleFunc("/owner", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(s.cfg.Server.Owner))
	})
	mux.HandleFunc("/logs/{knot}/{rkey}/{name}", s.Logs)

	mux.Mount("/xrpc", s.XrpcRouter())
	return mux
}

func (s *Spindle) XrpcRouter() http.Handler {
	logger := s.l.With("route", "xrpc")

	serviceAuth := serviceauth.NewServiceAuth(s.l, s.res, s.cfg.Server.Did().String())

	x := xrpc.Xrpc{
		Logger:      logger,
		Db:          s.db,
		Enforcer:    s.e,
		Engines:     s.engs,
		Config:      s.cfg,
		Resolver:    s.res,
		Vault:       s.vault,
		ServiceAuth: serviceAuth,
	}

	return x.Router()
}

func (s *Spindle) processPipeline(ctx context.Context, src eventconsumer.Source, msg eventconsumer.Message) error {
	if msg.Nsid == tangled.PipelineNSID {
		tpl := tangled.Pipeline{}
		err := json.Unmarshal(msg.EventJson, &tpl)
		if err != nil {
			fmt.Println("error unmarshalling", err)
			return err
		}

		if tpl.TriggerMetadata == nil {
			return fmt.Errorf("no trigger metadata found")
		}

		if tpl.TriggerMetadata.Repo == nil {
			return fmt.Errorf("no repo data found")
		}

		if src.Key() != tpl.TriggerMetadata.Repo.Knot {
			return fmt.Errorf("repo knot does not match event source: %s != %s", src.Key(), tpl.TriggerMetadata.Repo.Knot)
		}

		// filter by repos
		_, err = s.db.GetRepo(
			tpl.TriggerMetadata.Repo.Knot,
			tpl.TriggerMetadata.Repo.Did,
			tpl.TriggerMetadata.Repo.Repo,
		)
		if err != nil {
			return err
		}

		pipelineId := models.PipelineId{
			Knot: src.Key(),
			Rkey: msg.Rkey,
		}

		workflows := make(map[models.Engine][]models.Workflow)

		for _, w := range tpl.Workflows {
			if w != nil {
				if _, ok := s.engs[w.Engine]; !ok {
					err = s.db.StatusFailed(models.WorkflowId{
						PipelineId: pipelineId,
						Name:       w.Name,
					}, fmt.Sprintf("unknown engine %#v", w.Engine), -1, s.n)
					if err != nil {
						return err
					}

					continue
				}

				eng := s.engs[w.Engine]

				if _, ok := workflows[eng]; !ok {
					workflows[eng] = []models.Workflow{}
				}

				ewf, err := s.engs[w.Engine].InitWorkflow(*w, tpl)
				if err != nil {
					return err
				}

				workflows[eng] = append(workflows[eng], *ewf)

				err = s.db.StatusPending(models.WorkflowId{
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
				engine.StartWorkflows(s.l, s.vault, s.cfg, s.db, s.n, ctx, &models.Pipeline{
					RepoOwner: tpl.TriggerMetadata.Repo.Did,
					RepoName:  tpl.TriggerMetadata.Repo.Repo,
					Workflows: workflows,
				}, pipelineId)
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

	existing, err := s.e.GetSpindleUsersByRole("server:owner", rbacDomain)
	if err != nil {
		return err
	}

	switch len(existing) {
	case 0:
		// no owner configured, continue
	case 1:
		// find existing owner
		existingOwner := existing[0]

		// no ownership change, this is okay
		if existingOwner == s.cfg.Server.Owner {
			break
		}

		// remove existing owner
		err = s.e.RemoveSpindleOwner(rbacDomain, existingOwner)
		if err != nil {
			return nil
		}
	default:
		return fmt.Errorf("more than one owner in DB, try deleting %q and starting over", s.cfg.Server.DBPath)
	}

	return s.e.AddSpindleOwner(rbacDomain, cfgOwner)
}
