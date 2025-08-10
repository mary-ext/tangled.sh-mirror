{
  nixpkgs,
  system,
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
        config,
        pkgs,
        ...
      }: {
        nixos-shell = {
          inheritPath = false;
          mounts = {
            mountHome = false;
            mountNixProfile = false;
          };
        };
        virtualisation = {
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
        };
        services.getty.autologinUser = "root";
        environment.systemPackages = with pkgs; [curl vim git];
        systemd.tmpfiles.rules = let
          u = config.services.tangled-knot.gitUser;
          g = config.services.tangled-knot.gitUser;
        in [
          "d /var/lib/knot 0770 ${u} ${g} - -" # Create the directory first
          "f+ /var/lib/knot/secret 0660 ${u} ${g} - KNOT_SERVER_SECRET=${envVar "TANGLED_VM_KNOT_SECRET"}"
        ];
        services.tangled-knot = {
          enable = true;
          motd = "Welcome to the development knot!\n";
          server = {
            secretFile = "/var/lib/knot/secret";
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
      })
    ];
  }
