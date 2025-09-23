package nixery

import (
	"fmt"
	"path"
	"strings"

	"tangled.org/core/api/tangled"
	"tangled.org/core/workflow"
)

func nixConfStep() Step {
	setupCmd := `mkdir -p /etc/nix
echo 'extra-experimental-features = nix-command flakes' >> /etc/nix/nix.conf
echo 'build-users-group = ' >> /etc/nix/nix.conf`
	return Step{
		command: setupCmd,
		name:    "Configure Nix",
	}
}

// cloneOptsAsSteps processes clone options and adds corresponding steps
// to the beginning of the workflow's step list if cloning is not skipped.
//
// the steps to do here are:
// - git init
// - git remote add origin <url>
// - git fetch --depth=<d> --recurse-submodules=<yes|no> <sha>
// - git checkout FETCH_HEAD
func cloneStep(twf tangled.Pipeline_Workflow, tr tangled.Pipeline_TriggerMetadata, dev bool) Step {
	if twf.Clone.Skip {
		return Step{}
	}

	var commands []string

	// initialize git repo in workspace
	commands = append(commands, "git init")

	// add repo as git remote
	scheme := "https://"
	if dev {
		scheme = "http://"
		tr.Repo.Knot = strings.ReplaceAll(tr.Repo.Knot, "localhost", "host.docker.internal")
	}
	url := scheme + path.Join(tr.Repo.Knot, tr.Repo.Did, tr.Repo.Repo)
	commands = append(commands, fmt.Sprintf("git remote add origin %s", url))

	// run git fetch
	{
		var fetchArgs []string

		// default clone depth is 1
		depth := 1
		if twf.Clone.Depth > 1 {
			depth = int(twf.Clone.Depth)
		}
		fetchArgs = append(fetchArgs, fmt.Sprintf("--depth=%d", depth))

		// optionally recurse submodules
		if twf.Clone.Submodules {
			fetchArgs = append(fetchArgs, "--recurse-submodules=yes")
		}

		// set remote to fetch from
		fetchArgs = append(fetchArgs, "origin")

		// set revision to checkout
		switch workflow.TriggerKind(tr.Kind) {
		case workflow.TriggerKindManual:
			// TODO: unimplemented
		case workflow.TriggerKindPush:
			fetchArgs = append(fetchArgs, tr.Push.NewSha)
		case workflow.TriggerKindPullRequest:
			fetchArgs = append(fetchArgs, tr.PullRequest.SourceSha)
		}

		commands = append(commands, fmt.Sprintf("git fetch %s", strings.Join(fetchArgs, " ")))
	}

	// run git checkout
	commands = append(commands, "git checkout FETCH_HEAD")

	cloneStep := Step{
		command: strings.Join(commands, "\n"),
		name:    "Clone repository into workspace",
	}
	return cloneStep
}

// dependencyStep processes dependencies defined in the workflow.
// For dependencies using a custom registry (i.e. not nixpkgs), it collects
// all packages and adds a single 'nix profile install' step to the
// beginning of the workflow's step list.
func dependencyStep(deps map[string][]string) *Step {
	var customPackages []string

	for registry, packages := range deps {
		if registry == "nixpkgs" {
			continue
		}

		if len(packages) == 0 {
			customPackages = append(customPackages, registry)
		}
		// collect packages from custom registries
		for _, pkg := range packages {
			customPackages = append(customPackages, fmt.Sprintf("'%s#%s'", registry, pkg))
		}
	}

	if len(customPackages) > 0 {
		installCmd := "nix --extra-experimental-features nix-command --extra-experimental-features flakes profile install"
		cmd := fmt.Sprintf("%s %s", installCmd, strings.Join(customPackages, " "))
		installStep := Step{
			command: cmd,
			name:    "Install custom dependencies",
			environment: map[string]string{
				"NIX_NO_COLOR":               "1",
				"NIX_SHOW_DOWNLOAD_PROGRESS": "0",
			},
		}
		return &installStep
	}
	return nil
}
