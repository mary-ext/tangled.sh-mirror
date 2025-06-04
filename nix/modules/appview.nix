{self}: {
  config,
  pkgs,
  lib,
  ...
}:
with lib; {
  options = {
    services.tangled-appview = {
      enable = mkOption {
        type = types.bool;
        default = false;
        description = "Enable tangled appview";
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

  config = mkIf config.services.tangled-appview.enable {
    systemd.services.tangled-appview = {
      description = "tangled appview service";
      wantedBy = ["multi-user.target"];

      serviceConfig = {
        ListenStream = "0.0.0.0:${toString config.services.tangled-appview.port}";
        ExecStart = "${self.packages.${pkgs.system}.appview}/bin/appview";
        Restart = "always";
      };

      environment = {
        TANGLED_DB_PATH = "appview.db";
        TANGLED_COOKIE_SECRET = config.services.tangled-appview.cookie_secret;
      };
    };
  };
}
