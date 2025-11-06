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
        port = mkOption {
          type = types.int;
          default = 3000;
          description = "Port to run the appview on";
        };
        environmentFile = mkOption {
          type = with types; nullOr path;
          default = null;
          example = "/etc-/appview.env";
          description = ''
            Additional environment file as defined in {manpage}`systemd.exec(5)`.

            Sensitive secrets such as {env}`TANGLED_COOKIE_SECRET` may be
            passed to the service without makeing them world readable in the
            nix store.

          '';
        };
      };
    };

    config = mkIf cfg.enable {
      systemd.services.tangled.appview = {
        description = "tangled appview service";
        wantedBy = ["multi-user.target"];

        serviceConfig = {
          ListenStream = "0.0.0.0:${toString cfg.port}";
          ExecStart = "${cfg.package}/bin/appview";
          Restart = "always";
          EnvironmentFile = optional (cfg.environmentFile != null) cfg.environmentFile;
        };

        environment = {
          TANGLED_DB_PATH = "appview.db";
        };
      };
    };
  }
