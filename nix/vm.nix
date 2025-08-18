{
  nixpkgs,
  system,
  hostSystem,
  self,
}: let
  envVar = name: let
    var = builtins.getEnv name;
  in
    if var == ""
    then throw "\$${name} must be defined, see docs/hacking.md for more details"
    else var;
in
  nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      self.nixosModules.knot
      self.nixosModules.spindle
      ({
        lib,
        config,
        pkgs,
        ...
      }: {
        virtualisation.vmVariant.virtualisation = {
          host.pkgs = import nixpkgs {system = hostSystem;};

          graphics = false;
          memorySize = 2048;
          diskSize = 10 * 1024;
          cores = 2;
          forwardPorts = [
            # ssh
            {
              from = "host";
              host.port = 2222;
              guest.port = 22;
            }
            # knot
            {
              from = "host";
              host.port = 6000;
              guest.port = 6000;
            }
            # spindle
            {
              from = "host";
              host.port = 6555;
              guest.port = 6555;
            }
          ];
          sharedDirectories = {
            # We can't use the 9p mounts directly for most of these
            # as SQLite is incompatible with them. So instead we
            # mount the shared directories to a different location
            # and copy the contents around on service start/stop.
            knotData = {
              source = "$TANGLED_VM_DATA_DIR/knot";
              target = "/mnt/knot-data";
            };
            spindleData = {
              source = "$TANGLED_VM_DATA_DIR/spindle";
              target = "/mnt/spindle-data";
            };
            spindleLogs = {
              source = "$TANGLED_VM_DATA_DIR/spindle-logs";
              target = "/var/log/spindle";
            };
          };
        };
        # This is fine because any and all ports that are forwarded to host are explicitly marked above, we don't need a separate guest firewall
        networking.firewall.enable = false;
        time.timeZone = "Europe/London";
        services.getty.autologinUser = "root";
        environment.systemPackages = with pkgs; [curl vim git sqlite litecli];
        services.tangled-knot = {
          enable = true;
          motd = "Welcome to the development knot!\n";
          server = {
            owner = envVar "TANGLED_VM_KNOT_OWNER";
            hostname = "localhost:6000";
            listenAddr = "0.0.0.0:6000";
          };
        };
        services.tangled-spindle = {
          enable = true;
          server = {
            owner = envVar "TANGLED_VM_SPINDLE_OWNER";
            hostname = "localhost:6555";
            listenAddr = "0.0.0.0:6555";
            dev = true;
            secrets = {
              provider = "sqlite";
            };
          };
        };
        users = {
          # So we don't have to deal with permission clashing between
          # blank disk VMs and existing state
          users.${config.services.tangled-knot.gitUser}.uid = 666;
          groups.${config.services.tangled-knot.gitUser}.gid = 666;

          # TODO: separate spindle user
        };
        systemd.services = let
          mkDataSyncScripts = source: target: {
            enableStrictShellChecks = true;

            preStart = lib.mkBefore ''
              mkdir -p ${target}
              ${lib.getExe pkgs.rsync} -a ${source}/ ${target}
            '';

            postStop = lib.mkAfter ''
              ${lib.getExe pkgs.rsync} -a ${target}/ ${source}
            '';

            serviceConfig.PermissionsStartOnly = true;
          };
        in {
          knot = mkDataSyncScripts "/mnt/knot-data" config.services.tangled-knot.stateDir;
          spindle = mkDataSyncScripts "/mnt/spindle-data" (builtins.dirOf config.services.tangled-spindle.server.dbPath);
        };
      })
    ];
  }
