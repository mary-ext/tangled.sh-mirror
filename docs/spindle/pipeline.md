# spindle pipeline manifest

Spindle pipelines are defined under the `.tangled/workflows` directory in a
repo. Generally:

* Pipelines are defined in YAML.
* Workflows can run using different *engines*.

The most barebones workflow looks like this:

```yaml
when:
  - event: ["push"]
    branch: ["main"]

engine: "nixery"

# optional
clone:
  skip: false
  depth: 50
  submodules: true
```

The `when` and `engine` fields are required, while every other aspect
of how the definition is parsed is up to the engine. Currently, a spindle
provides at least one of these built-in engines:

## `nixery`

The Nixery engine uses an instance of [Nixery](https://nixery.dev) to run
steps that use dependencies from [Nixpkgs](https://github.com/NixOS/nixpkgs).

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
