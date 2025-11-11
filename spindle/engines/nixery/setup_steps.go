package nixery

import (
	"fmt"
	"strings"
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
