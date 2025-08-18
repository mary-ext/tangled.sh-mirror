{
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

        package = mkOption {
          type = types.package;
          description = "Package to use for the knot";
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

        motd = mkOption {
          type = types.nullOr types.str;
          default = null;
          description = ''
            Message of the day

            The contents are shown as-is; eg. you will want to add a newline if
            setting a non-empty message since the knot won't do this for you.
          '';
        };

        motdFile = mkOption {
          type = types.nullOr types.path;
          default = null;
          description = ''
            File containing message of the day

            The contents are shown as-is; eg. you will want to add a newline if
            setting a non-empty message since the knot won't do this for you.
          '';
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

          owner = mkOption {
            type = types.str;
            example = "did:plc:qfpnj4og54vl56wngdriaxug";
            description = "DID of owner (required)";
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
      environment.systemPackages = [
        pkgs.git
        cfg.package
      ];

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
          ${cfg.package}/bin/knot keys \
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
        enableStrictShellChecks = true;

        preStart = let
          setMotd =
            if cfg.motdFile != null && cfg.motd != null
            then throw "motdFile and motd cannot be both set"
            else ''
              ${optionalString (cfg.motdFile != null) "cat ${cfg.motdFile} > ${cfg.stateDir}/motd"}
              ${optionalString (cfg.motd != null) ''printf "${cfg.motd}" > ${cfg.stateDir}/motd''}
            '';
        in ''
          mkdir -p "${cfg.repo.scanPath}"
          chown -R ${cfg.gitUser}:${cfg.gitUser} "${cfg.repo.scanPath}"

          mkdir -p "${cfg.stateDir}/.config/git"
          cat > "${cfg.stateDir}/.config/git/config" << EOF
          [user]
              name = Git User
              email = git@example.com
          [receive]
              advertisePushOptions = true
          EOF
          ${setMotd}
          chown -R ${cfg.gitUser}:${cfg.gitUser} "${cfg.stateDir}"
        '';

        serviceConfig = {
          User = cfg.gitUser;
          PermissionsStartOnly = true;
          WorkingDirectory = cfg.stateDir;
          Environment = [
            "KNOT_REPO_SCAN_PATH=${cfg.repo.scanPath}"
            "KNOT_REPO_MAIN_BRANCH=${cfg.repo.mainBranch}"
            "APPVIEW_ENDPOINT=${cfg.appviewEndpoint}"
            "KNOT_SERVER_INTERNAL_LISTEN_ADDR=${cfg.server.internalListenAddr}"
            "KNOT_SERVER_LISTEN_ADDR=${cfg.server.listenAddr}"
            "KNOT_SERVER_DB_PATH=${cfg.server.dbPath}"
            "KNOT_SERVER_HOSTNAME=${cfg.server.hostname}"
            "KNOT_SERVER_OWNER=${cfg.server.owner}"
          ];
          EnvironmentFile = cfg.server.secretFile;
          ExecStart = "${cfg.package}/bin/knot server";
          Restart = "always";
        };
      };

      networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [22];
    };
  }
