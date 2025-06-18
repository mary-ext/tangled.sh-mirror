package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
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

	chanMu      sync.RWMutex
	stdoutChans map[string]chan string
	stderrChans map[string]chan string

	cleanupMu sync.Mutex
	cleanup   map[string][]cleanupFunc
}

func New(ctx context.Context, db *db.DB, n *notifier.Notifier) (*Engine, error) {
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
	}

	e.stdoutChans = make(map[string]chan string, 100)
	e.stderrChans = make(map[string]chan string, 100)

	e.cleanup = make(map[string][]cleanupFunc)

	return e, nil
}

func (e *Engine) StartWorkflows(ctx context.Context, pipeline *tangled.Pipeline, pipelineId models.PipelineId) {
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

			// TODO: actual checks for image/registry etc.
			var deps string
			for _, d := range w.Dependencies {
				if d.Registry == "nixpkgs" {
					deps = path.Join(d.Packages...)
				}
			}

			// load defaults from somewhere else
			deps = path.Join(deps, "bash", "git", "coreutils", "nix")

			cimg := path.Join("nixery.dev", deps)
			reader, err := e.docker.ImagePull(ctx, cimg, image.PullOptions{})
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

			err = e.StartSteps(ctx, w.Steps, wid, cimg)
			if err != nil {
				e.l.Error("workflow failed!", "wid", wid.String(), "error", err.Error())

				err := e.db.StatusFailed(wid, err.Error(), -1, e.n)
				if err != nil {
					return err
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
func (e *Engine) StartSteps(ctx context.Context, steps []*tangled.Pipeline_Step, wid models.WorkflowId, image string) error {
	// set up logging channels
	e.chanMu.Lock()
	if _, exists := e.stdoutChans[wid.String()]; !exists {
		e.stdoutChans[wid.String()] = make(chan string, 100)
	}
	if _, exists := e.stderrChans[wid.String()]; !exists {
		e.stderrChans[wid.String()] = make(chan string, 100)
	}
	e.chanMu.Unlock()

	// close channels after all steps are complete
	defer func() {
		close(e.stdoutChans[wid.String()])
		close(e.stderrChans[wid.String()])
	}()

	for _, step := range steps {
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

		wg := sync.WaitGroup{}

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.TailStep(ctx, resp.ID, wid)
			if err != nil {
				e.l.Error("failed to tail container", "container", resp.ID)
				return
			}
		}()

		// wait until all logs are piped
		wg.Wait()

		state, err := e.WaitStep(ctx, resp.ID)
		if err != nil {
			return err
		}

		err = e.DestroyStep(ctx, resp.ID)
		if err != nil {
			return err
		}

		if state.ExitCode != 0 {
			e.l.Error("workflow failed!", "workflow_id", wid.String(), "error", state.Error, "exit_code", state.ExitCode)
			return fmt.Errorf("%s", state.Error)
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

func (e *Engine) TailStep(ctx context.Context, containerID string, wid models.WorkflowId) error {
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

	// using StdCopy we demux logs and stream stdout and stderr to different
	// channels.
	//
	//    stdout w||r stdoutCh
	//    stderr w||r stderrCh
	//

	rpipeOut, wpipeOut := io.Pipe()
	rpipeErr, wpipeErr := io.Pipe()

	go func() {
		defer wpipeOut.Close()
		defer wpipeErr.Close()
		_, err := stdcopy.StdCopy(wpipeOut, wpipeErr, logs)
		if err != nil && err != io.EOF {
			e.l.Error("failed to copy logs", "error", err)
		}
	}()

	// read from stdout and send to stdout pipe
	// NOTE: the stdoutCh channnel is closed further up in StartSteps
	// once all steps are done.
	go func() {
		e.chanMu.RLock()
		stdoutCh := e.stdoutChans[wid.String()]
		e.chanMu.RUnlock()

		scanner := bufio.NewScanner(rpipeOut)
		for scanner.Scan() {
			stdoutCh <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			e.l.Error("failed to scan stdout", "error", err)
		}
	}()

	// read from stderr and send to stderr pipe
	// NOTE: the stderrCh channnel is closed further up in StartSteps
	// once all steps are done.
	go func() {
		e.chanMu.RLock()
		stderrCh := e.stderrChans[wid.String()]
		e.chanMu.RUnlock()

		scanner := bufio.NewScanner(rpipeErr)
		for scanner.Scan() {
			stderrCh <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			e.l.Error("failed to scan stderr", "error", err)
		}
	}()

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
			e.l.Error("failed to cleanup workflow resource", "workflowId", wid)
		}
	}
	return nil
}

func (e *Engine) LogChannels(wid models.WorkflowId) (stdout <-chan string, stderr <-chan string, ok bool) {
	e.chanMu.RLock()
	defer e.chanMu.RUnlock()

	stdoutCh, ok1 := e.stdoutChans[wid.String()]
	stderrCh, ok2 := e.stderrChans[wid.String()]

	if !ok1 || !ok2 {
		return nil, nil, false
	}
	return stdoutCh, stderrCh, true
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
		},
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
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
