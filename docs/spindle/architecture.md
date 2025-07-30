# spindle architecture

Spindle is a small CI runner service. Here's a high level overview of how it operates:

* listens for [`sh.tangled.spindle.member`](/lexicons/spindle/member.json) and
[`sh.tangled.repo`](/lexicons/repo.json) records on the Jetstream.
* when a new repo record comes through (typically when you add a spindle to a
repo from the settings), spindle then resolves the underlying knot and
subscribes to repo events (see:
[`sh.tangled.pipeline`](/lexicons/pipeline.json)).
* the spindle engine then handles execution of the pipeline, with results and
logs beamed on the spindle event stream over wss

### the engine

At present, the only supported backend is Docker (and Podman, if Docker
compatibility is enabled, so that `/run/docker.sock` is created). Spindle
executes each step in the pipeline in a fresh container, with state persisted
across steps within the `/tangled/workspace` directory.

The base image for the container is constructed on the fly using
[Nixery](https://nixery.dev), which is handy for caching layers for frequently
used packages.

The pipeline manifest is [specified here](/docs/spindle/pipeline.md).
