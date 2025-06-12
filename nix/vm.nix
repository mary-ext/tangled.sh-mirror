{
  nixpkgs,
  self,
}:
nixpkgs.lib.nixosSystem {
  system = "x86_64-linux";
  modules = [
    self.nixosModules.knot
    ({
      config,
      pkgs,
      ...
    }: {
      virtualisation.memorySize = 2048;
      virtualisation.diskSize = 10 * 1024;
      virtualisation.cores = 2;
      services.getty.autologinUser = "root";
      environment.systemPackages = with pkgs; [curl vim git];
      systemd.tmpfiles.rules = let
        u = config.services.tangled-knot.gitUser;
        g = config.services.tangled-knot.gitUser;
      in [
        "d /var/lib/knot 0770 ${u} ${g} - -" # Create the directory first
        "f+ /var/lib/knot/secret 0660 ${u} ${g} - KNOT_SERVER_SECRET=2650ecafdce279b09865fb1923051156eb773ee7485061b2e766086f07dbd85a"
      ];
      services.tangled-knot = {
        enable = true;
        server = {
          secretFile = "/var/lib/knot/secret";
          hostname = "localhost:6000";
          listenAddr = "0.0.0.0:6000";
        };
      };
    })
  ];
}
