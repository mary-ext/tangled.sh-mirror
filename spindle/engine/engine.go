package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/sync/errgroup"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/notifier"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/spindle/db"
)

const (
	workspaceDir = "/tangled/workspace"
)

type Engine struct {
	docker client.APIClient
	l      *slog.Logger
	db     *db.DB
	n      *notifier.Notifier
}

func New(ctx context.Context, db *db.DB, n *notifier.Notifier) (*Engine, error) {
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	l := log.FromContext(ctx).With("component", "spindle")

	return &Engine{docker: dcli, l: l, db: db, n: n}, nil
}

// SetupPipeline sets up a new network for the pipeline, and possibly volumes etc.
// in the future. In here also goes other setup steps.
func (e *Engine) SetupPipeline(ctx context.Context, pipeline *tangled.Pipeline, atUri, id string) error {
	e.l.Info("setting up pipeline", "pipeline", id)

	_, err := e.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name:   workspaceVolume(id),
		Driver: "local",
	})
	if err != nil {
		return err
	}

	_, err = e.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name:   nixVolume(id),
		Driver: "local",
	})
	if err != nil {
		return err
	}

	_, err = e.docker.NetworkCreate(ctx, pipelineName(id), network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return err
	}

	err = e.db.CreatePipeline(id, atUri, e.n)
	return err
}

func (e *Engine) StartWorkflows(ctx context.Context, pipeline *tangled.Pipeline, id string) error {
	e.l.Info("starting all workflows in parallel", "pipeline", id)

	err := e.db.MarkPipelineRunning(id, e.n)
	if err != nil {
		return err
	}

	g := errgroup.Group{}
	for _, w := range pipeline.Workflows {
		g.Go(func() error {
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
				e.l.Error("pipeline failed!", "id", id, "error", err.Error())
				err := e.db.MarkPipelineFailed(id, -1, err.Error(), e.n)
				if err != nil {
					return err
				}
				return fmt.Errorf("pulling image: %w", err)
			}
			defer reader.Close()
			io.Copy(os.Stdout, reader)

			err = e.StartSteps(ctx, w.Steps, id, cimg)
			if err != nil {
				e.l.Error("pipeline failed!", "id", id, "error", err.Error())
				return e.db.MarkPipelineFailed(id, -1, err.Error(), e.n)
			}

			return nil
		})
	}

	err = g.Wait()
	if err != nil {
		e.l.Error("pipeline failed!", "id", id, "error", err.Error())
		return e.db.MarkPipelineFailed(id, -1, err.Error(), e.n)
	}

	e.l.Info("pipeline success!", "id", id)
	return e.db.MarkPipelineSuccess(id, e.n)
}

// StartSteps starts all steps sequentially with the same base image.
// ONLY marks pipeline as failed if container's exit code is non-zero.
// All other errors are bubbled up.
func (e *Engine) StartSteps(ctx context.Context, steps []*tangled.Pipeline_Step, id, image string) error {
	for _, step := range steps {
		hostConfig := hostConfig(id)
		resp, err := e.docker.ContainerCreate(ctx, &container.Config{
			Image:      image,
			Cmd:        []string{"bash", "-c", step.Command},
			WorkingDir: workspaceDir,
			Tty:        false,
			Hostname:   "spindle",
			Env:        []string{"HOME=" + workspaceDir},
		}, hostConfig, nil, nil, "")
		if err != nil {
			return fmt.Errorf("creating container: %w", err)
		}

		err = e.docker.NetworkConnect(ctx, pipelineName(id), resp.ID, nil)
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
			err := e.TailStep(ctx, resp.ID)
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

		if state.ExitCode != 0 {
			e.l.Error("pipeline failed!", "id", id, "error", state.Error, "exit_code", state.ExitCode)
			return e.db.MarkPipelineFailed(id, state.ExitCode, state.Error, e.n)
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

func (e *Engine) TailStep(ctx context.Context, containerID string) error {
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

	go func() {
		_, _ = stdcopy.StdCopy(os.Stdout, os.Stdout, logs)
		_ = logs.Close()
	}()
	return nil
}

func workspaceVolume(id string) string {
	return "workspace-" + id
}

func nixVolume(id string) string {
	return "nix-" + id
}

func pipelineName(id string) string {
	return "pipeline-" + id
}

func hostConfig(id string) *container.HostConfig {
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: workspaceVolume(id),
				Target: workspaceDir,
			},
			{
				Type:   mount.TypeVolume,
				Source: nixVolume(id),
				Target: "/nix",
			},
		},
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
	}

	return hostConfig
}
