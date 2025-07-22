{
  config,
  lib,
  ...
}: let
  cfg = config.services.tangled-appview;
in
  with lib; {
    options = {
      services.tangled-appview = {
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
        cookie_secret = mkOption {
          type = types.str;
          default = "00000000000000000000000000000000";
          description = "Cookie secret";
        };
      };
    };

    config = mkIf cfg.enable {
      systemd.services.tangled-appview = {
        description = "tangled appview service";
        wantedBy = ["multi-user.target"];

        serviceConfig = {
          ListenStream = "0.0.0.0:${toString cfg.port}";
          ExecStart = "${cfg.package}/bin/appview";
          Restart = "always";
        };

        environment = {
          TANGLED_DB_PATH = "appview.db";
          TANGLED_COOKIE_SECRET = cfg.cookie_secret;
        };
      };
    };
  }
