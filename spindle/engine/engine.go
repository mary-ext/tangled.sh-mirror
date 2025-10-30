package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	securejoin "github.com/cyphar/filepath-securejoin"
	"golang.org/x/sync/errgroup"
	"tangled.org/core/notifier"
	"tangled.org/core/spindle/config"
	"tangled.org/core/spindle/db"
	"tangled.org/core/spindle/models"
	"tangled.org/core/spindle/secrets"
)

var (
	ErrTimedOut       = errors.New("timed out")
	ErrWorkflowFailed = errors.New("workflow failed")
)

func StartWorkflows(l *slog.Logger, vault secrets.Manager, cfg *config.Config, db *db.DB, n *notifier.Notifier, ctx context.Context, pipeline *models.Pipeline, pipelineId models.PipelineId) {
	l.Info("starting all workflows in parallel", "pipeline", pipelineId)

	// extract secrets
	var allSecrets []secrets.UnlockedSecret
	if didSlashRepo, err := securejoin.SecureJoin(pipeline.RepoOwner, pipeline.RepoName); err == nil {
		if res, err := vault.GetSecretsUnlocked(ctx, secrets.DidSlashRepo(didSlashRepo)); err == nil {
			allSecrets = res
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	for eng, wfs := range pipeline.Workflows {
		workflowTimeout := eng.WorkflowTimeout()
		l.Info("using workflow timeout", "timeout", workflowTimeout)

		for _, w := range wfs {
			eg.Go(func() error {
				wid := models.WorkflowId{
					PipelineId: pipelineId,
					Name:       w.Name,
				}

				err := db.StatusRunning(wid, n)
				if err != nil {
					return err
				}

				err = eng.SetupWorkflow(ctx, wid, &w)
				if err != nil {
					// TODO(winter): Should this always set StatusFailed?
					// In the original, we only do in a subset of cases.
					l.Error("setting up worklow", "wid", wid, "err", err)

					destroyErr := eng.DestroyWorkflow(ctx, wid)
					if destroyErr != nil {
						l.Error("failed to destroy workflow after setup failure", "error", destroyErr)
					}

					dbErr := db.StatusFailed(wid, err.Error(), -1, n)
					if dbErr != nil {
						return dbErr
					}
					return err
				}
				defer eng.DestroyWorkflow(ctx, wid)

				wfLogger, err := models.NewWorkflowLogger(cfg.Server.LogDir, wid)
				if err != nil {
					l.Warn("failed to setup step logger; logs will not be persisted", "error", err)
					wfLogger = nil
				} else {
					defer wfLogger.Close()
				}

				ctx, cancel := context.WithTimeout(ctx, workflowTimeout)
				defer cancel()

				for stepIdx, step := range w.Steps {
					// log start of step
					if wfLogger != nil {
						wfLogger.
							ControlWriter(stepIdx, step, models.StepStatusStart).
							Write([]byte{0})
					}

					err = eng.RunStep(ctx, wid, &w, stepIdx, allSecrets, wfLogger)

					// log end of step
					if wfLogger != nil {
						wfLogger.
							ControlWriter(stepIdx, step, models.StepStatusEnd).
							Write([]byte{0})
					}

					if err != nil {
						if errors.Is(err, ErrTimedOut) {
							dbErr := db.StatusTimeout(wid, n)
							if dbErr != nil {
								return dbErr
							}
						} else {
							dbErr := db.StatusFailed(wid, err.Error(), -1, n)
							if dbErr != nil {
								return dbErr
							}
						}

						return fmt.Errorf("starting steps image: %w", err)
					}
				}

				err = db.StatusSuccess(wid, n)
				if err != nil {
					return err
				}

				return nil
			})
		}
	}

	if err := eg.Wait(); err != nil {
		l.Error("failed to run one or more workflows", "err", err)
	} else {
		l.Info("successfully ran full pipeline")
	}
}
