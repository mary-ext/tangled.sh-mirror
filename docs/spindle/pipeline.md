# spindle pipeline manifest

Spindle pipelines are defined under the `.tangled/workflows` directory in a
repo. Generally:

* Pipelines are defined in YAML.
* Dependencies can be specified from
[Nixpkgs](https://search.nixos.org) or custom registries.
* Environment variables can be set globally or per-step.

Here's an example that uses all fields:

```yaml
# build_and_test.yaml
when:
  - event: ["push", "pull_request"]
    branch: ["main", "develop"]
  - event: ["manual"]

dependencies:
  ## from nixpkgs
  nixpkgs:
    - nodejs
  ## custom registry
  git+https://tangled.sh/@oppi.li/statix:
    - statix

steps:
  - name: "Install dependencies"
    command: "npm install"
    environment:
      NODE_ENV: "development"
      CI: "true"

  - name: "Run linter"
    command: "npm run lint"

  - name: "Run tests"
    command: "npm test"
    environment:
      NODE_ENV: "test"
      JEST_WORKERS: "2"

  - name: "Build application"
    command: "npm run build"
    environment:
      NODE_ENV: "production"

environment:
  BUILD_NUMBER: "123"
  GIT_BRANCH: "main"

## current repository is cloned and checked out at the target ref
## by default.
clone:
  skip: false
  depth: 50
  submodules: true
```

## git push options

These are push options that can be used with the `--push-option (-o)` flag of git push:

- `verbose-ci`, `ci-verbose`: enables diagnostics reporting for the CI pipeline, allowing you to see any issues when you push.
- `skip-ci`, `ci-skip`: skips triggering the CI pipeline.
