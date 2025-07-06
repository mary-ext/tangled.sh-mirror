package models

import (
	"fmt"
	"path"
	"strings"

	"tangled.sh/tangled.sh/core/api/tangled"
)

func nixConfStep() Step {
	setupCmd := `echo 'extra-experimental-features = nix-command flakes' >> /etc/nix/nix.conf
echo 'build-users-group = ' >> /etc/nix/nix.conf`
	return Step{
		Command: setupCmd,
		Name:    "Configure Nix",
	}
}

// checkoutStep checks out the specified ref in the cloned repository.
func checkoutStep(twf tangled.Pipeline_Workflow, tr tangled.Pipeline_TriggerMetadata) Step {
	if twf.Clone.Skip {
		return Step{}
	}

	var ref string
	switch tr.Kind {
	case "push":
		ref = tr.Push.Ref
	case "pull_request":
		ref = tr.PullRequest.TargetBranch

	// TODO: this needs to be specified in lexicon
	case "manual":
		ref = tr.Repo.DefaultBranch
	}

	checkoutCmd := fmt.Sprintf("git config advice.detachedHead false; git checkout --progress --force %s", ref)

	return Step{
		Command: checkoutCmd,
		Name:    "Checkout ref " + ref,
	}
}

// cloneOptsAsSteps processes clone options and adds corresponding steps
// to the beginning of the workflow's step list if cloning is not skipped.
func cloneStep(twf tangled.Pipeline_Workflow, tr tangled.Pipeline_TriggerRepo, dev bool) Step {
	if twf.Clone.Skip {
		return Step{}
	}

	uri := "https://"
	if dev {
		uri = "http://"
		tr.Knot = strings.ReplaceAll(tr.Knot, "localhost", "host.docker.internal")
	}

	cloneUrl := uri + path.Join(tr.Knot, tr.Did, tr.Repo)
	cloneCmd := []string{"git", "clone", cloneUrl, "."}

	// default clone depth is 1
	cloneDepth := 1
	if twf.Clone.Depth > 1 {
		cloneDepth = int(twf.Clone.Depth)
	}
	cloneCmd = append(cloneCmd, []string{"--depth", fmt.Sprintf("%d", cloneDepth)}...)

	if twf.Clone.Submodules {
		cloneCmd = append(cloneCmd, "--recursive")
	}

	fmt.Println(strings.Join(cloneCmd, " "))

	cloneStep := Step{
		Command: strings.Join(cloneCmd, " "),
		Name:    "Clone repository into workspace",
	}
	return cloneStep
}

// dependencyStep processes dependencies defined in the workflow.
// For dependencies using a custom registry (i.e. not nixpkgs), it collects
// all packages and adds a single 'nix profile install' step to the
// beginning of the workflow's step list.
func dependencyStep(twf tangled.Pipeline_Workflow) Step {
	var customPackages []string

	for _, d := range twf.Dependencies {
		registry := d.Registry
		packages := d.Packages

		if registry == "nixpkgs" {
			continue
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
			Command: cmd,
			Name:    "Install custom dependencies",
			Environment: map[string]string{
				"NIX_NO_COLOR":               "1",
				"NIX_SHOW_DOWNLOAD_PROGRESS": "0",
			},
		}
		return installStep
	}
	return Step{}
}
