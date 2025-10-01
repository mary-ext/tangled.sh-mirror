# spindle pipelines

Spindle workflows allow you to write CI/CD pipelines in a simple format. They're located in the `.tangled/workflows` directory at the root of your repository, and are defined using YAML.

The fields are:

- [Trigger](#trigger): A **required** field that defines when a workflow should be triggered.
- [Engine](#engine): A **required** field that defines which engine a workflow should run on.
- [Clone options](#clone-options): An **optional** field that defines how the repository should be cloned.
- [Dependencies](#dependencies): An **optional** field that allows you to list dependencies you may need.
- [Environment](#environment): An **optional** field that allows you to define environment variables.
- [Steps](#steps): An **optional** field that allows you to define what steps should run in the workflow.

## Trigger

The first thing to add to a workflow is the trigger, which defines when a workflow runs. This is defined using a `when` field, which takes in a list of conditions. Each condition has the following fields:

- `event`: This is a **required** field that defines when your workflow should run. It's a list that can take one or more of the following values:
  - `push`: The workflow should run every time a commit is pushed to the repository.
  - `pull_request`: The workflow should run every time a pull request is made or updated.
  - `manual`: The workflow can be triggered manually.
- `branch`: This is a **required** field that defines which branches the workflow should run for. If used with the `push` event, commits to the branch(es) listed here will trigger the workflow. If used with the `pull_request` event, updates to pull requests targeting the branch(es) listed here will trigger the workflow. This field has no effect with the `manual` event.

For example, if you'd like to define a workflow that runs when commits are pushed to the `main` and `develop` branches, or when pull requests that target the `main` branch are updated, or manually, you can do so with:

```yaml
when:
  - event: ["push", "manual"]
    branch: ["main", "develop"]
  - event: ["pull_request"]
    branch: ["main"]
```

## Engine

Next is the engine on which the workflow should run, defined using the **required** `engine` field. The currently supported engines are:

- `nixery`: This uses an instance of [Nixery](https://nixery.dev) to run steps, which allows you to add [dependencies](#dependencies) from [Nixpkgs](https://github.com/NixOS/nixpkgs). You can search for packages on https://search.nixos.org, and there's a pretty good chance the package(s) you're looking for will be there.

Example:

```yaml
engine: "nixery"
```

## Clone options

When a workflow starts, the first step is to clone the repository. You can customize this behavior using the **optional** `clone` field. It has the following fields:

- `skip`: Setting this to `true` will skip cloning the repository. This can be useful if your workflow is doing something that doesn't require anything from the repository itself. This is `false` by default.
- `depth`: This sets the number of commits, or the "clone depth", to fetch from the repository. For example, if you set this to 2, the last 2 commits will be fetched. By default, the depth is set to 1, meaning only the most recent commit will be fetched, which is the commit that triggered the workflow.
- `submodules`: If you use [git submodules](https://git-scm.com/book/en/v2/Git-Tools-Submodules) in your repository, setting this field to `true` will recursively fetch all submodules. This is `false` by default.

The default settings are:

```yaml
clone:
  skip: false
  depth: 1
  submodules: false
```

## Dependencies

Usually when you're running a workflow, you'll need additional dependencies. The `dependencies` field lets you define which dependencies to get, and from where. It's a key-value map, with the key being the registry to fetch dependencies from, and the value being the list of dependencies to fetch.

Say you want to fetch Node.js and Go from `nixpkgs`, and a package called `my_pkg` you've made from your own registry at your repository at `https://tangled.sh/@example.com/my_pkg`. You can define those dependencies like so:

```yaml
dependencies:
  # nixpkgs
  nixpkgs:
    - nodejs
    - go
  # custom registry
  git+https://tangled.org/@example.com/my_pkg:
    - my_pkg
```

Now these dependencies are available to use in your workflow!

## Environment

The `environment` field allows you define environment variables that will be available throughout the entire workflow. **Do not put secrets here, these environment variables are visible to anyone viewing the repository. You can add secrets for pipelines in your repository's settings.**

Example:

```yaml
environment:
  GOOS: "linux"
  GOARCH: "arm64"
  NODE_ENV: "production"
  MY_ENV_VAR: "MY_ENV_VALUE"
```

## Steps

The `steps` field allows you to define what steps should run in the workflow. It's a list of step objects, each with the following fields:

- `name`: This field allows you to give your step a name. This name is visible in your workflow runs, and is used to describe what the step is doing.
- `command`: This field allows you to define a command to run in that step. The step is run in a Bash shell, and the logs from the command will be visible in the pipelines page on the Tangled website. The [dependencies](#dependencies) you added will be available to use here.
- `environment`: Similar to the global [environment](#environment) config, this **optional** field is a key-value map that allows you to set environment variables for the step. **Do not put secrets here, these environment variables are visible to anyone viewing the repository. You can add secrets for pipelines in your repository's settings.**

Example:

```yaml
steps:
  - name: "Build backend"
    command: "go build"
    environment:
      GOOS: "darwin"
      GOARCH: "arm64"
  - name: "Build frontend"
    command: "npm run build"
    environment:
      NODE_ENV: "production"
```

## Complete workflow

```yaml
# .tangled/workflows/build.yml

when:
  - event: ["push", "manual"]
    branch: ["main", "develop"]
  - event: ["pull_request"]
    branch: ["main"]

engine: "nixery"

# using the default values
clone:
  skip: false
  depth: 1
  submodules: false

dependencies:
  # nixpkgs
  nixpkgs:
    - nodejs
    - go
  # custom registry
  git+https://tangled.org/@example.com/my_pkg:
    - my_pkg

environment:
  GOOS: "linux"
  GOARCH: "arm64"
  NODE_ENV: "production"
  MY_ENV_VAR: "MY_ENV_VALUE"

steps:
  - name: "Build backend"
    command: "go build"
    environment:
      GOOS: "darwin"
      GOARCH: "arm64"
  - name: "Build frontend"
    command: "npm run build"
    environment:
      NODE_ENV: "production"
```

If you want another example of a workflow, you can look at the one [Tangled uses to build the project](https://tangled.sh/@tangled.sh/core/blob/master/.tangled/workflows/build.yml).
