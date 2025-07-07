package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/models"
)

const (
	workspaceDir = "/tangled/workspace"
)

type cleanupFunc func(context.Context) error

type Engine struct {
	docker client.APIClient
	l      *slog.Logger
	db     *db.DB
	n      *notifier.Notifier
	cfg    *config.Config

	cleanupMu sync.Mutex
	cleanup   map[string][]cleanupFunc
}

func New(ctx context.Context, cfg *config.Config, db *db.DB, n *notifier.Notifier) (*Engine, error) {
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	l := log.FromContext(ctx).With("component", "spindle")

	e := &Engine{
		docker: dcli,
		l:      l,
		db:     db,
		n:      n,
		cfg:    cfg,
	}

	e.cleanup = make(map[string][]cleanupFunc)

	return e, nil
}

func (e *Engine) StartWorkflows(ctx context.Context, pipeline *models.Pipeline, pipelineId models.PipelineId) {
	e.l.Info("starting all workflows in parallel", "pipeline", pipelineId)

	wg := sync.WaitGroup{}
	for _, w := range pipeline.Workflows {
		wg.Add(1)
		go func() error {
			defer wg.Done()
			wid := models.WorkflowId{
				PipelineId: pipelineId,
				Name:       w.Name,
			}

			err := e.db.StatusRunning(wid, e.n)
			if err != nil {
				return err
			}

			err = e.SetupWorkflow(ctx, wid)
			if err != nil {
				e.l.Error("setting up worklow", "wid", wid, "err", err)
				return err
			}
			defer e.DestroyWorkflow(ctx, wid)

			reader, err := e.docker.ImagePull(ctx, w.Image, image.PullOptions{})
			if err != nil {
				e.l.Error("pipeline failed!", "workflowId", wid, "error", err.Error())

				err := e.db.StatusFailed(wid, err.Error(), -1, e.n)
				if err != nil {
					return err
				}

				return fmt.Errorf("pulling image: %w", err)
			}
			defer reader.Close()
			io.Copy(os.Stdout, reader)

			workflowTimeoutStr := e.cfg.Pipelines.WorkflowTimeout
			workflowTimeout, err := time.ParseDuration(workflowTimeoutStr)
			if err != nil {
				e.l.Error("failed to parse workflow timeout", "error", err, "timeout", workflowTimeoutStr)
				workflowTimeout = 5 * time.Minute
			}
			e.l.Info("using workflow timeout", "timeout", workflowTimeout)
			ctx, cancel := context.WithTimeout(ctx, workflowTimeout)
			defer cancel()

			err = e.StartSteps(ctx, w.Steps, wid, w.Image)
			if err != nil {
				if errors.Is(err, ErrTimedOut) {
					dbErr := e.db.StatusTimeout(wid, e.n)
					if dbErr != nil {
						return dbErr
					}
				} else {
					dbErr := e.db.StatusFailed(wid, err.Error(), -1, e.n)
					if dbErr != nil {
						return dbErr
					}
				}

				return fmt.Errorf("starting steps image: %w", err)
			}

			err = e.db.StatusSuccess(wid, e.n)
			if err != nil {
				return err
			}

			return nil
		}()
	}

	wg.Wait()
}

// SetupWorkflow sets up a new network for the workflow and volumes for
// the workspace and Nix store. These are persisted across steps and are
// destroyed at the end of the workflow.
func (e *Engine) SetupWorkflow(ctx context.Context, wid models.WorkflowId) error {
	e.l.Info("setting up workflow", "workflow", wid)

	_, err := e.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name:   workspaceVolume(wid),
		Driver: "local",
	})
	if err != nil {
		return err
	}
	e.registerCleanup(wid, func(ctx context.Context) error {
		return e.docker.VolumeRemove(ctx, workspaceVolume(wid), true)
	})

	_, err = e.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name:   nixVolume(wid),
		Driver: "local",
	})
	if err != nil {
		return err
	}
	e.registerCleanup(wid, func(ctx context.Context) error {
		return e.docker.VolumeRemove(ctx, nixVolume(wid), true)
	})

	_, err = e.docker.NetworkCreate(ctx, networkName(wid), network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return err
	}
	e.registerCleanup(wid, func(ctx context.Context) error {
		return e.docker.NetworkRemove(ctx, networkName(wid))
	})

	return nil
}

// StartSteps starts all steps sequentially with the same base image.
// ONLY marks pipeline as failed if container's exit code is non-zero.
// All other errors are bubbled up.
// Fixed version of the step execution logic
func (e *Engine) StartSteps(ctx context.Context, steps []models.Step, wid models.WorkflowId, image string) error {

	for stepIdx, step := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		envs := ConstructEnvs(step.Environment)
		envs.AddEnv("HOME", workspaceDir)
		e.l.Debug("envs for step", "step", step.Name, "envs", envs.Slice())

		hostConfig := hostConfig(wid)
		resp, err := e.docker.ContainerCreate(ctx, &container.Config{
			Image:      image,
			Cmd:        []string{"bash", "-c", step.Command},
			WorkingDir: workspaceDir,
			Tty:        false,
			Hostname:   "spindle",
			Env:        envs.Slice(),
		}, hostConfig, nil, nil, "")
		defer e.DestroyStep(ctx, resp.ID)
		if err != nil {
			return fmt.Errorf("creating container: %w", err)
		}

		err = e.docker.NetworkConnect(ctx, networkName(wid), resp.ID, nil)
		if err != nil {
			return fmt.Errorf("connecting network: %w", err)
		}

		err = e.docker.ContainerStart(ctx, resp.ID, container.StartOptions{})
		if err != nil {
			return err
		}
		e.l.Info("started container", "name", resp.ID, "step", step.Name)

		// start tailing logs in background
		tailDone := make(chan error, 1)
		go func() {
			tailDone <- e.TailStep(ctx, resp.ID, wid, stepIdx, step)
		}()

		// wait for container completion or timeout
		waitDone := make(chan struct{})
		var state *container.State
		var waitErr error

		go func() {
			defer close(waitDone)
			state, waitErr = e.WaitStep(ctx, resp.ID)
		}()

		select {
		case <-waitDone:

			// wait for tailing to complete
			<-tailDone

		case <-ctx.Done():
			e.l.Warn("step timed out; killing container", "container", resp.ID, "step", step.Name)
			err = e.DestroyStep(context.Background(), resp.ID)
			if err != nil {
				e.l.Error("failed to destroy step", "container", resp.ID, "error", err)
			}

			// wait for both goroutines to finish
			<-waitDone
			<-tailDone

			return ErrTimedOut
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if waitErr != nil {
			return waitErr
		}

		err = e.DestroyStep(ctx, resp.ID)
		if err != nil {
			return err
		}

		if state.ExitCode != 0 {
			e.l.Error("workflow failed!", "workflow_id", wid.String(), "error", state.Error, "exit_code", state.ExitCode, "oom_killed", state.OOMKilled)
			if state.OOMKilled {
				return ErrOOMKilled
			}
			return ErrWorkflowFailed
		}
	}

	return nil
}

func (e *Engine) WaitStep(ctx context.Context, containerID string) (*container.State, error) {
	wait, errCh := e.docker.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	case <-wait:
	}

	e.l.Info("waited for container", "name", containerID)

	info, err := e.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}

	return info.State, nil
}

func (e *Engine) TailStep(ctx context.Context, containerID string, wid models.WorkflowId, stepIdx int, step models.Step) error {
	wfLogger, err := NewWorkflowLogger(e.cfg.Pipelines.LogDir, wid)
	if err != nil {
		e.l.Warn("failed to setup step logger; logs will not be persisted", "error", err)
		return err
	}
	defer wfLogger.Close()

	ctl := wfLogger.ControlWriter(stepIdx, step)
	ctl.Write([]byte(step.Name))

	logs, err := e.docker.ContainerLogs(ctx, containerID, container.LogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
		Details:    false,
		Timestamps: false,
	})
	if err != nil {
		return err
	}

	_, err = stdcopy.StdCopy(
		wfLogger.DataWriter("stdout"),
		wfLogger.DataWriter("stderr"),
		logs,
	)
	if err != nil && err != io.EOF && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("failed to copy logs: %w", err)
	}

	return nil
}

func (e *Engine) DestroyStep(ctx context.Context, containerID string) error {
	err := e.docker.ContainerKill(ctx, containerID, "9") // SIGKILL
	if err != nil && !isErrContainerNotFoundOrNotRunning(err) {
		return err
	}

	if err := e.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		RemoveLinks:   false,
		Force:         false,
	}); err != nil && !isErrContainerNotFoundOrNotRunning(err) {
		return err
	}

	return nil
}

func (e *Engine) DestroyWorkflow(ctx context.Context, wid models.WorkflowId) error {
	e.cleanupMu.Lock()
	key := wid.String()

	fns := e.cleanup[key]
	delete(e.cleanup, key)
	e.cleanupMu.Unlock()

	for _, fn := range fns {
		if err := fn(ctx); err != nil {
			e.l.Error("failed to cleanup workflow resource", "workflowId", wid, "error", err)
		}
	}
	return nil
}

func (e *Engine) registerCleanup(wid models.WorkflowId, fn cleanupFunc) {
	e.cleanupMu.Lock()
	defer e.cleanupMu.Unlock()

	key := wid.String()
	e.cleanup[key] = append(e.cleanup[key], fn)
}

func workspaceVolume(wid models.WorkflowId) string {
	return fmt.Sprintf("workspace-%s", wid)
}

func nixVolume(wid models.WorkflowId) string {
	return fmt.Sprintf("nix-%s", wid)
}

func networkName(wid models.WorkflowId) string {
	return fmt.Sprintf("workflow-network-%s", wid)
}

func hostConfig(wid models.WorkflowId) *container.HostConfig {
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: workspaceVolume(wid),
				Target: workspaceDir,
			},
			{
				Type:   mount.TypeVolume,
				Source: nixVolume(wid),
				Target: "/nix",
			},
			{
				Type:     mount.TypeTmpfs,
				Target:   "/tmp",
				ReadOnly: false,
				TmpfsOptions: &mount.TmpfsOptions{
					Mode: 0o1777, // world-writeable sticky bit
				},
			},
			{
				Type:   mount.TypeVolume,
				Source: "etc-nix-" + wid.String(),
				Target: "/etc/nix",
			},
		},
		ReadonlyRootfs: false,
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{"CAP_DAC_OVERRIDE"},
		SecurityOpt:    []string{"no-new-privileges"},
		ExtraHosts:     []string{"host.docker.internal:host-gateway"},
	}

	return hostConfig
}

// thanks woodpecker
func isErrContainerNotFoundOrNotRunning(err error) bool {
	// Error response from daemon: Cannot kill container: ...: No such container: ...
	// Error response from daemon: Cannot kill container: ...: Container ... is not running"
	// Error response from podman daemon: can only kill running containers. ... is in state exited
	// Error: No such container: ...
	return err != nil && (strings.Contains(err.Error(), "No such container") || strings.Contains(err.Error(), "is not running") || strings.Contains(err.Error(), "can only kill running containers"))
}
