package nixery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
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
	image string
	env   map[string]string
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

// SetupWorkflow sets up a new network for the workflow and volumes for
// the workspace and Nix store. These are persisted across steps and are
// destroyed at the end of the workflow.
func (e *Engine) SetupWorkflow(ctx context.Context, wid models.WorkflowId, wf *models.Workflow) error {
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

	addl := wf.Data.(addlFields)

	reader, err := e.docker.ImagePull(ctx, addl.image, image.PullOptions{})
	if err != nil {
		e.l.Error("pipeline image pull failed!", "image", addl.image, "workflowId", wid, "error", err.Error())

		return fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()
	io.Copy(os.Stdout, reader)

	return nil
}

func (e *Engine) RunStep(ctx context.Context, wid models.WorkflowId, w *models.Workflow, idx int, secrets []secrets.UnlockedSecret, wfLogger *models.WorkflowLogger) error {
	workflowEnvs := ConstructEnvs(w.Data.(addlFields).env)
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
	envs.AddEnv("HOME", workspaceDir)
	e.l.Debug("envs for step", "step", step.Name, "envs", envs.Slice())

	hostConfig := hostConfig(wid)
	resp, err := e.docker.ContainerCreate(ctx, &container.Config{
		Image:      w.Data.(addlFields).image,
		Cmd:        []string{"bash", "-c", step.command},
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
		tailDone <- e.tailStep(ctx, wfLogger, resp.ID, wid, idx, step)
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

		return engine.ErrTimedOut
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
		return engine.ErrWorkflowFailed
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

func (e *Engine) tailStep(ctx context.Context, wfLogger *models.WorkflowLogger, containerID string, wid models.WorkflowId, stepIdx int, step models.Step) error {
	if wfLogger == nil {
		return nil
	}

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
					Options: [][]string{
						{"exec"},
					},
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
