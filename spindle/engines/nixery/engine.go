package nixery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"runtime"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"gopkg.in/yaml.v3"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/models"
	"tangled.sh/tangled.sh/core/spindle/secrets"
)

const (
	workspaceDir = "/tangled/workspace"
	homeDir      = "/tangled/home"
)

type cleanupFunc func(context.Context) error

type Engine struct {
	docker client.APIClient
	l      *slog.Logger
	cfg    *config.Config

	cleanupMu sync.Mutex
	cleanup   map[string][]cleanupFunc
}

type Step struct {
	name        string
	kind        models.StepKind
	command     string
	environment map[string]string
}

func (s Step) Name() string {
	return s.name
}

func (s Step) Command() string {
	return s.command
}

func (s Step) Kind() models.StepKind {
	return s.kind
}

// setupSteps get added to start of Steps
type setupSteps []models.Step

// addStep adds a step to the beginning of the workflow's steps.
func (ss *setupSteps) addStep(step models.Step) {
	*ss = append(*ss, step)
}

type addlFields struct {
	image     string
	container string
	env       map[string]string
}

func (e *Engine) InitWorkflow(twf tangled.Pipeline_Workflow, tpl tangled.Pipeline) (*models.Workflow, error) {
	swf := &models.Workflow{}
	addl := addlFields{}

	dwf := &struct {
		Steps []struct {
			Command     string            `yaml:"command"`
			Name        string            `yaml:"name"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"steps"`
		Dependencies map[string][]string `yaml:"dependencies"`
		Environment  map[string]string   `yaml:"environment"`
	}{}
	err := yaml.Unmarshal([]byte(twf.Raw), &dwf)
	if err != nil {
		return nil, err
	}

	for _, dstep := range dwf.Steps {
		sstep := Step{}
		sstep.environment = dstep.Environment
		sstep.command = dstep.Command
		sstep.name = dstep.Name
		sstep.kind = models.StepKindUser
		swf.Steps = append(swf.Steps, sstep)
	}
	swf.Name = twf.Name
	addl.env = dwf.Environment
	addl.image = workflowImage(dwf.Dependencies, e.cfg.NixeryPipelines.Nixery)

	setup := &setupSteps{}

	setup.addStep(nixConfStep())
	setup.addStep(cloneStep(twf, *tpl.TriggerMetadata, e.cfg.Server.Dev))
	// this step could be empty
	if s := dependencyStep(dwf.Dependencies); s != nil {
		setup.addStep(*s)
	}

	// append setup steps in order to the start of workflow steps
	swf.Steps = append(*setup, swf.Steps...)
	swf.Data = addl

	return swf, nil
}

func (e *Engine) WorkflowTimeout() time.Duration {
	workflowTimeoutStr := e.cfg.NixeryPipelines.WorkflowTimeout
	workflowTimeout, err := time.ParseDuration(workflowTimeoutStr)
	if err != nil {
		e.l.Error("failed to parse workflow timeout", "error", err, "timeout", workflowTimeoutStr)
		workflowTimeout = 5 * time.Minute
	}

	return workflowTimeout
}

func workflowImage(deps map[string][]string, nixery string) string {
	var dependencies string
	for reg, ds := range deps {
		if reg == "nixpkgs" {
			dependencies = path.Join(ds...)
		}
	}

	// load defaults from somewhere else
	dependencies = path.Join(dependencies, "bash", "git", "coreutils", "nix")

	if runtime.GOARCH == "arm64" {
		dependencies = path.Join("arm64", dependencies)
	}

	return path.Join(nixery, dependencies)
}

func New(ctx context.Context, cfg *config.Config) (*Engine, error) {
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	l := log.FromContext(ctx).With("component", "spindle")

	e := &Engine{
		docker: dcli,
		l:      l,
		cfg:    cfg,
	}

	e.cleanup = make(map[string][]cleanupFunc)

	return e, nil
}

func (e *Engine) SetupWorkflow(ctx context.Context, wid models.WorkflowId, wf *models.Workflow) error {
	e.l.Info("setting up workflow", "workflow", wid)

	_, err := e.docker.NetworkCreate(ctx, networkName(wid), network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return err
	}
	e.registerCleanup(wid, func(ctx context.Context) error {
		return e.docker.NetworkRemove(ctx, networkName(wid))
	})

	addl := wf.Data.(addlFields)

	reader, err := e.docker.ImagePull(ctx, addl.image, image.PullOptions{})
	if err != nil {
		e.l.Error("pipeline image pull failed!", "image", addl.image, "workflowId", wid, "error", err.Error())

		return fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()
	io.Copy(os.Stdout, reader)

	resp, err := e.docker.ContainerCreate(ctx, &container.Config{
		Image:      addl.image,
		Cmd:        []string{"cat"},
		OpenStdin:  true, // so cat stays alive :3
		Tty:        false,
		Hostname:   "spindle",
		WorkingDir: workspaceDir,
		Labels: map[string]string{
			"sh.tangled.pipeline/workflow_id": wid.String(),
		},
		// TODO(winter): investigate whether environment variables passed here
		// get propagated to ContainerExec processes
	}, &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeTmpfs,
				Target:   "/tmp",
				ReadOnly: false,
				TmpfsOptions: &mount.TmpfsOptions{
					Mode: 0o1777, // world-writeable sticky bit
					Options: [][]string{
						{"exec"},
					},
				},
			},
		},
		ReadonlyRootfs: false,
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{"CAP_DAC_OVERRIDE"},
		SecurityOpt:    []string{"no-new-privileges"},
		ExtraHosts:     []string{"host.docker.internal:host-gateway"},
	}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	e.registerCleanup(wid, func(ctx context.Context) error {
		err = e.docker.ContainerStop(ctx, resp.ID, container.StopOptions{})
		if err != nil {
			return err
		}

		return e.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{
			RemoveVolumes: true,
			RemoveLinks:   false,
			Force:         false,
		})
	})

	err = e.docker.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	mkExecResp, err := e.docker.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
		Cmd:          []string{"mkdir", "-p", workspaceDir, homeDir},
		AttachStdout: true, // NOTE(winter): pretty sure this will make it so that when stdout read is done below, mkdir is done. maybe??
		AttachStderr: true, // for good measure, backed up by docker/cli ("If -d is not set, attach to everything by default")
	})
	if err != nil {
		return err
	}

	// This actually *starts* the command. Thanks, Docker!
	execResp, err := e.docker.ContainerExecAttach(ctx, mkExecResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer execResp.Close()

	// This is apparently best way to wait for the command to complete.
	_, err = io.ReadAll(execResp.Reader)
	if err != nil {
		return err
	}

	execInspectResp, err := e.docker.ContainerExecInspect(ctx, mkExecResp.ID)
	if err != nil {
		return err
	}

	if execInspectResp.ExitCode != 0 {
		return fmt.Errorf("mkdir exited with exit code %d", execInspectResp.ExitCode)
	} else if execInspectResp.Running {
		return errors.New("mkdir is somehow still running??")
	}

	addl.container = resp.ID
	wf.Data = addl

	return nil
}

func (e *Engine) RunStep(ctx context.Context, wid models.WorkflowId, w *models.Workflow, idx int, secrets []secrets.UnlockedSecret, wfLogger *models.WorkflowLogger) error {
	addl := w.Data.(addlFields)
	workflowEnvs := ConstructEnvs(addl.env)
	// TODO(winter): should SetupWorkflow also have secret access?
	// IMO yes, but probably worth thinking on.
	for _, s := range secrets {
		workflowEnvs.AddEnv(s.Key, s.Value)
	}

	step := w.Steps[idx].(Step)

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	envs := append(EnvVars(nil), workflowEnvs...)
	for k, v := range step.environment {
		envs.AddEnv(k, v)
	}
	envs.AddEnv("HOME", homeDir)

	mkExecResp, err := e.docker.ContainerExecCreate(ctx, addl.container, container.ExecOptions{
		Cmd:          []string{"bash", "-c", step.command},
		AttachStdout: true,
		AttachStderr: true,
		Env:          envs,
	})
	if err != nil {
		return fmt.Errorf("creating exec: %w", err)
	}

	// start tailing logs in background
	tailDone := make(chan error, 1)
	go func() {
		tailDone <- e.tailStep(ctx, wfLogger, mkExecResp.ID, wid, idx, step)
	}()

	select {
	case <-tailDone:

	case <-ctx.Done():
		// cleanup will be handled by DestroyWorkflow, since
		// Docker doesn't provide an API to kill an exec run
		// (sure, we could grab the PID and kill it ourselves,
		// but that's wasted effort)
		e.l.Warn("step timed out", "step", step.Name)

		<-tailDone

		return engine.ErrTimedOut
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	execInspectResp, err := e.docker.ContainerExecInspect(ctx, mkExecResp.ID)
	if err != nil {
		return err
	}

	if execInspectResp.ExitCode != 0 {
		inspectResp, err := e.docker.ContainerInspect(ctx, addl.container)
		if err != nil {
			return err
		}

		e.l.Error("workflow failed!", "workflow_id", wid.String(), "exit_code", execInspectResp.ExitCode, "oom_killed", inspectResp.State.OOMKilled)

		if inspectResp.State.OOMKilled {
			return ErrOOMKilled
		}
		return engine.ErrWorkflowFailed
	}

	return nil
}

func (e *Engine) tailStep(ctx context.Context, wfLogger *models.WorkflowLogger, execID string, wid models.WorkflowId, stepIdx int, step models.Step) error {
	if wfLogger == nil {
		return nil
	}

	// This actually *starts* the command. Thanks, Docker!
	logs, err := e.docker.ContainerExecAttach(ctx, execID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer logs.Close()

	_, err = stdcopy.StdCopy(
		wfLogger.DataWriter("stdout"),
		wfLogger.DataWriter("stderr"),
		logs.Reader,
	)
	if err != nil && err != io.EOF && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("failed to copy logs: %w", err)
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

func networkName(wid models.WorkflowId) string {
	return fmt.Sprintf("workflow-network-%s", wid)
}
