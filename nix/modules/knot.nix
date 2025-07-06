{self}: {
  config,
  pkgs,
  lib,
  ...
}: let
  cfg = config.services.tangled-knot;
in
  with lib; {
    options = {
      services.tangled-knot = {
        enable = mkOption {
          type = types.bool;
          default = false;
          description = "Enable a tangled knot";
        };

        appviewEndpoint = mkOption {
          type = types.str;
          default = "https://tangled.sh";
          description = "Appview endpoint";
        };

        gitUser = mkOption {
          type = types.str;
          default = "git";
          description = "User that hosts git repos and performs git operations";
        };

        openFirewall = mkOption {
          type = types.bool;
          default = true;
          description = "Open port 22 in the firewall for ssh";
        };

        stateDir = mkOption {
          type = types.path;
          default = "/home/${cfg.gitUser}";
          description = "Tangled knot data directory";
        };

        repo = {
          scanPath = mkOption {
            type = types.path;
            default = cfg.stateDir;
            description = "Path where repositories are scanned from";
          };

          mainBranch = mkOption {
            type = types.str;
            default = "main";
            description = "Default branch name for repositories";
          };
        };

        server = {
          listenAddr = mkOption {
            type = types.str;
            default = "0.0.0.0:5555";
            description = "Address to listen on";
          };

          internalListenAddr = mkOption {
            type = types.str;
            default = "127.0.0.1:5444";
            description = "Internal address for inter-service communication";
          };

          secretFile = mkOption {
            type = lib.types.path;
            example = "KNOT_SERVER_SECRET=<hash>";
            description = "File containing secret key provided by appview (required)";
          };

          dbPath = mkOption {
            type = types.path;
            default = "${cfg.stateDir}/knotserver.db";
            description = "Path to the database file";
          };

          hostname = mkOption {
            type = types.str;
            example = "knot.tangled.sh";
            description = "Hostname for the server (required)";
          };

          dev = mkOption {
            type = types.bool;
            default = false;
            description = "Enable development mode (disables signature verification)";
          };
        };
      };
    };

    config = mkIf cfg.enable {
      environment.systemPackages = with pkgs; [
        git
        self.packages."${pkgs.system}".knot
      ];

      system.activationScripts.gitConfig = ''
        mkdir -p "${cfg.repo.scanPath}"
        chown -R ${cfg.gitUser}:${cfg.gitUser} "${cfg.repo.scanPath}"

        mkdir -p "${cfg.stateDir}/.config/git"
        cat > "${cfg.stateDir}/.config/git/config" << EOF
        [user]
            name = Git User
            email = git@example.com
        EOF
        chown -R ${cfg.gitUser}:${cfg.gitUser} "${cfg.stateDir}"
      '';

      users.users.${cfg.gitUser} = {
        isSystemUser = true;
        useDefaultShell = true;
        home = cfg.stateDir;
        createHome = true;
        group = cfg.gitUser;
      };

      users.groups.${cfg.gitUser} = {};

      services.openssh = {
        enable = true;
        extraConfig = ''
          Match User ${cfg.gitUser}
              AuthorizedKeysCommand /etc/ssh/keyfetch_wrapper
              AuthorizedKeysCommandUser nobody
        '';
      };

      environment.etc."ssh/keyfetch_wrapper" = {
        mode = "0555";
        text = ''
          #!${pkgs.stdenv.shell}
          ${self.packages.${pkgs.system}.knot}/bin/knot keys \
            -output authorized-keys \
            -internal-api "http://${cfg.server.internalListenAddr}" \
            -git-dir "${cfg.repo.scanPath}" \
            -log-path /tmp/knotguard.log
        '';
      };

      systemd.services.knot = {
        description = "knot service";
        after = ["network.target" "sshd.service"];
        wantedBy = ["multi-user.target"];
        serviceConfig = {
          User = cfg.gitUser;
          WorkingDirectory = cfg.stateDir;
          Environment = [
            "KNOT_REPO_SCAN_PATH=${cfg.repo.scanPath}"
            "KNOT_REPO_MAIN_BRANCH=${cfg.repo.mainBranch}"
            "APPVIEW_ENDPOINT=${cfg.appviewEndpoint}"
            "KNOT_SERVER_INTERNAL_LISTEN_ADDR=${cfg.server.internalListenAddr}"
            "KNOT_SERVER_LISTEN_ADDR=${cfg.server.listenAddr}"
            "KNOT_SERVER_DB_PATH=${cfg.server.dbPath}"
            "KNOT_SERVER_HOSTNAME=${cfg.server.hostname}"
          ];
          EnvironmentFile = cfg.server.secretFile;
          ExecStart = "${self.packages.${pkgs.system}.knot}/bin/knot server";
          Restart = "always";
        };
      };

      networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [22];
    };
  }
