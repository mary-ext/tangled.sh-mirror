# spindle self-hosting guide

## prerequisites

* Go
* Docker (the only supported backend currently)

## configuration

Spindle is configured using environment variables. The following environment variables are available:

* `SPINDLE_SERVER_LISTEN_ADDR`: The address the server listens on (default: `"0.0.0.0:6555"`).
* `SPINDLE_SERVER_DB_PATH`: The path to the SQLite database file (default: `"spindle.db"`).
* `SPINDLE_SERVER_HOSTNAME`: The hostname of the server (required).
* `SPINDLE_SERVER_JETSTREAM_ENDPOINT`: The endpoint of the Jetstream server (default: `"wss://jetstream1.us-west.bsky.network/subscribe"`).
* `SPINDLE_SERVER_DEV`: A boolean indicating whether the server is running in development mode (default: `false`).
* `SPINDLE_SERVER_OWNER`: The DID of the owner (required).
* `SPINDLE_PIPELINES_NIXERY`: The Nixery URL (default: `"nixery.tangled.sh"`).
* `SPINDLE_PIPELINES_WORKFLOW_TIMEOUT`: The default workflow timeout (default: `"5m"`).
* `SPINDLE_PIPELINES_LOG_DIR`: The directory to store workflow logs (default: `"/var/log/spindle"`).

## running spindle

1.  **Set the environment variables.**  For example:

    ```shell
    export SPINDLE_SERVER_HOSTNAME="your-hostname"
    export SPINDLE_SERVER_OWNER="your-did"
    ```

2.  **Build the Spindle binary.**

    ```shell
    cd core
    go mod download
    go build -o cmd/spindle/spindle cmd/spindle/main.go
    ```

3.  **Create the log directory.**

    ```shell
    sudo mkdir -p /var/log/spindle
    sudo chown $USER:$USER -R /var/log/spindle
    ```

4.  **Run the Spindle binary.**

    ```shell
    ./cmd/spindle/spindle
    ```

Spindle will now start, connect to the Jetstream server, and begin processing pipelines.
