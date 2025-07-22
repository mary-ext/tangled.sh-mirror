{
  config,
  lib,
  ...
}: let
  cfg = config.services.tangled-spindle;
in
  with lib; {
    options = {
      services.tangled-spindle = {
        enable = mkOption {
          type = types.bool;
          default = false;
          description = "Enable a tangled spindle";
        };
        package = mkOption {
          type = types.package;
          description = "Package to use for the spindle";
        };

        server = {
          listenAddr = mkOption {
            type = types.str;
            default = "0.0.0.0:6555";
            description = "Address to listen on";
          };

          dbPath = mkOption {
            type = types.path;
            default = "/var/lib/spindle/spindle.db";
            description = "Path to the database file";
          };

          hostname = mkOption {
            type = types.str;
            example = "spindle.tangled.sh";
            description = "Hostname for the server (required)";
          };

          jetstreamEndpoint = mkOption {
            type = types.str;
            default = "wss://jetstream1.us-west.bsky.network/subscribe";
            description = "Jetstream endpoint to subscribe to";
          };

          dev = mkOption {
            type = types.bool;
            default = false;
            description = "Enable development mode (disables signature verification)";
          };

          owner = mkOption {
            type = types.str;
            example = "did:plc:qfpnj4og54vl56wngdriaxug";
            description = "DID of owner (required)";
          };
        };

        pipelines = {
          nixery = mkOption {
            type = types.str;
            default = "nixery.tangled.sh";
            description = "Nixery instance to use";
          };

          workflowTimeout = mkOption {
            type = types.str;
            default = "5m";
            description = "Timeout for each step of a pipeline";
          };
        };
      };
    };

    config = mkIf cfg.enable {
      virtualisation.docker.enable = true;

      systemd.services.spindle = {
        description = "spindle service";
        after = ["network.target" "docker.service"];
        wantedBy = ["multi-user.target"];
        serviceConfig = {
          LogsDirectory = "spindle";
          StateDirectory = "spindle";
          Environment = [
            "SPINDLE_SERVER_LISTEN_ADDR=${cfg.server.listenAddr}"
            "SPINDLE_SERVER_DB_PATH=${cfg.server.dbPath}"
            "SPINDLE_SERVER_HOSTNAME=${cfg.server.hostname}"
            "SPINDLE_SERVER_JETSTREAM=${cfg.server.jetstreamEndpoint}"
            "SPINDLE_SERVER_DEV=${lib.boolToString cfg.server.dev}"
            "SPINDLE_SERVER_OWNER=${cfg.server.owner}"
            "SPINDLE_PIPELINES_NIXERY=${cfg.pipelines.nixery}"
            "SPINDLE_PIPELINES_WORKFLOW_TIMEOUT=${cfg.pipelines.workflowTimeout}"
          ];
          ExecStart = "${cfg.package}/bin/spindle";
          Restart = "always";
        };
      };
    };
  }
