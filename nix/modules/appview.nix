{
  config,
  lib,
  ...
}: let
  cfg = config.services.tangled.appview;
in
  with lib; {
    options = {
      services.tangled.appview = {
        enable = mkOption {
          type = types.bool;
          default = false;
          description = "Enable tangled appview";
        };

        package = mkOption {
          type = types.package;
          description = "Package to use for the appview";
        };

        # core configuration
        port = mkOption {
          type = types.port;
          default = 3000;
          description = "Port to run the appview on";
        };

        listenAddr = mkOption {
          type = types.str;
          default = "0.0.0.0:${toString cfg.port}";
          description = "Listen address for the appview service";
        };

        dbPath = mkOption {
          type = types.str;
          default = "/var/lib/appview/appview.db";
          description = "Path to the SQLite database file";
        };

        appviewHost = mkOption {
          type = types.str;
          default = "https://tangled.org";
          example = "https://example.com";
          description = "Public host URL for the appview instance";
        };

        appviewName = mkOption {
          type = types.str;
          default = "Tangled";
          description = "Display name for the appview instance";
        };

        dev = mkOption {
          type = types.bool;
          default = false;
          description = "Enable development mode";
        };

        disallowedNicknamesFile = mkOption {
          type = types.nullOr types.path;
          default = null;
          description = "Path to file containing disallowed nicknames";
        };

        # redis configuration
        redis = {
          addr = mkOption {
            type = types.str;
            default = "localhost:6379";
            description = "Redis server address";
          };

          db = mkOption {
            type = types.int;
            default = 0;
            description = "Redis database number";
          };
        };

        # jetstream configuration
        jetstream = {
          endpoint = mkOption {
            type = types.str;
            default = "wss://jetstream1.us-east.bsky.network/subscribe";
            description = "Jetstream WebSocket endpoint";
          };
        };

        # knotstream consumer configuration
        knotstream = {
          retryInterval = mkOption {
            type = types.str;
            default = "60s";
            description = "Initial retry interval for knotstream consumer";
          };

          maxRetryInterval = mkOption {
            type = types.str;
            default = "120m";
            description = "Maximum retry interval for knotstream consumer";
          };

          connectionTimeout = mkOption {
            type = types.str;
            default = "5s";
            description = "Connection timeout for knotstream consumer";
          };

          workerCount = mkOption {
            type = types.int;
            default = 64;
            description = "Number of workers for knotstream consumer";
          };

          queueSize = mkOption {
            type = types.int;
            default = 100;
            description = "Queue size for knotstream consumer";
          };
        };

        # spindlestream consumer configuration
        spindlestream = {
          retryInterval = mkOption {
            type = types.str;
            default = "60s";
            description = "Initial retry interval for spindlestream consumer";
          };

          maxRetryInterval = mkOption {
            type = types.str;
            default = "120m";
            description = "Maximum retry interval for spindlestream consumer";
          };

          connectionTimeout = mkOption {
            type = types.str;
            default = "5s";
            description = "Connection timeout for spindlestream consumer";
          };

          workerCount = mkOption {
            type = types.int;
            default = 64;
            description = "Number of workers for spindlestream consumer";
          };

          queueSize = mkOption {
            type = types.int;
            default = 100;
            description = "Queue size for spindlestream consumer";
          };
        };

        # resend configuration
        resend = {
          sentFrom = mkOption {
            type = types.str;
            default = "noreply@notifs.tangled.sh";
            description = "Email address to send notifications from";
          };
        };

        # posthog configuration
        posthog = {
          endpoint = mkOption {
            type = types.str;
            default = "https://eu.i.posthog.com";
            description = "PostHog API endpoint";
          };
        };

        # camo configuration
        camo = {
          host = mkOption {
            type = types.str;
            default = "https://camo.tangled.sh";
            description = "Camo proxy host URL";
          };
        };

        # avatar configuration
        avatar = {
          host = mkOption {
            type = types.str;
            default = "https://avatar.tangled.sh";
            description = "Avatar service host URL";
          };
        };

        plc = {
          url = mkOption {
            type = types.str;
            default = "https://plc.directory";
            description = "PLC directory URL";
          };
        };

        pds = {
          host = mkOption {
            type = types.str;
            default = "https://tngl.sh";
            description = "PDS host URL";
          };
        };

        label = {
          defaults = mkOption {
            type = types.listOf types.str;
            default = [
              "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/wontfix"
              "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/good-first-issue"
              "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/duplicate"
              "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/documentation"
              "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/assignee"
            ];
            description = "Default label definitions";
          };

          goodFirstIssue = mkOption {
            type = types.str;
            default = "at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.label.definition/good-first-issue";
            description = "Good first issue label definition";
          };
        };

        environmentFile = mkOption {
          type = with types; nullOr path;
          default = null;
          example = "/etc/appview.env";
          description = ''
            Additional environment file as defined in {manpage}`systemd.exec(5)`.

            Sensitive secrets such as {env}`TANGLED_COOKIE_SECRET`,
            {env}`TANGLED_OAUTH_CLIENT_SECRET`, {env}`TANGLED_RESEND_API_KEY`,
            {env}`TANGLED_CAMO_SHARED_SECRET`, {env}`TANGLED_AVATAR_SHARED_SECRET`,
            {env}`TANGLED_REDIS_PASS`, {env}`TANGLED_PDS_ADMIN_SECRET`,
            {env}`TANGLED_CLOUDFLARE_API_TOKEN`, {env}`TANGLED_CLOUDFLARE_ZONE_ID`,
            {env}`TANGLED_CLOUDFLARE_TURNSTILE_SITE_KEY`,
            {env}`TANGLED_CLOUDFLARE_TURNSTILE_SECRET_KEY`,
            {env}`TANGLED_POSTHOG_API_KEY`, {env}`TANGLED_APP_PASSWORD`,
            and {env}`TANGLED_ALT_APP_PASSWORD` may be passed to the service
            without making them world readable in the nix store.
          '';
        };
      };
    };

    config = mkIf cfg.enable {
      services.redis.servers.appview = {
        enable = true;
        port = 6379;
      };

      systemd.services.appview = {
        description = "tangled appview service";
        wantedBy = ["multi-user.target"];
        after = ["redis-appview.service" "network-online.target"];
        requires = ["redis-appview.service"];
        wants = ["network-online.target"];

        serviceConfig = {
          Type = "simple";
          ExecStart = "${cfg.package}/bin/appview";
          Restart = "always";
          RestartSec = "10s";
          EnvironmentFile = mkIf (cfg.environmentFile != null) cfg.environmentFile;

          # state directory
          StateDirectory = "appview";
          WorkingDirectory = "/var/lib/appview";

          # security hardening
          NoNewPrivileges = true;
          PrivateTmp = true;
          ProtectSystem = "strict";
          ProtectHome = true;
          ReadWritePaths = ["/var/lib/appview"];
        };

        environment =
          {
            TANGLED_DB_PATH = cfg.dbPath;
            TANGLED_LISTEN_ADDR = cfg.listenAddr;
            TANGLED_APPVIEW_HOST = cfg.appviewHost;
            TANGLED_APPVIEW_NAME = cfg.appviewName;
            TANGLED_DEV =
              if cfg.dev
              then "true"
              else "false";
          }
          // optionalAttrs (cfg.disallowedNicknamesFile != null) {
            TANGLED_DISALLOWED_NICKNAMES_FILE = cfg.disallowedNicknamesFile;
          }
          // {
            TANGLED_REDIS_ADDR = cfg.redis.addr;
            TANGLED_REDIS_DB = toString cfg.redis.db;

            TANGLED_JETSTREAM_ENDPOINT = cfg.jetstream.endpoint;

            TANGLED_KNOTSTREAM_RETRY_INTERVAL = cfg.knotstream.retryInterval;
            TANGLED_KNOTSTREAM_MAX_RETRY_INTERVAL = cfg.knotstream.maxRetryInterval;
            TANGLED_KNOTSTREAM_CONNECTION_TIMEOUT = cfg.knotstream.connectionTimeout;
            TANGLED_KNOTSTREAM_WORKER_COUNT = toString cfg.knotstream.workerCount;
            TANGLED_KNOTSTREAM_QUEUE_SIZE = toString cfg.knotstream.queueSize;

            TANGLED_SPINDLESTREAM_RETRY_INTERVAL = cfg.spindlestream.retryInterval;
            TANGLED_SPINDLESTREAM_MAX_RETRY_INTERVAL = cfg.spindlestream.maxRetryInterval;
            TANGLED_SPINDLESTREAM_CONNECTION_TIMEOUT = cfg.spindlestream.connectionTimeout;
            TANGLED_SPINDLESTREAM_WORKER_COUNT = toString cfg.spindlestream.workerCount;
            TANGLED_SPINDLESTREAM_QUEUE_SIZE = toString cfg.spindlestream.queueSize;

            TANGLED_RESEND_SENT_FROM = cfg.resend.sentFrom;

            TANGLED_POSTHOG_ENDPOINT = cfg.posthog.endpoint;

            TANGLED_CAMO_HOST = cfg.camo.host;

            TANGLED_AVATAR_HOST = cfg.avatar.host;

            TANGLED_PLC_URL = cfg.plc.url;

            TANGLED_PDS_HOST = cfg.pds.host;

            TANGLED_LABEL_DEFAULTS = concatStringsSep "," cfg.label.defaults;
            TANGLED_LABEL_GFI = cfg.label.goodFirstIssue;
          };
      };
    };
  }
