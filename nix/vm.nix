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
        "f+ /var/lib/knot/secret 0660 ${u} ${g} - KNOT_SERVER_SECRET=168c426fa6d9829fcbe85c96bdf144e800fb9737d6ca87f21acc543b1aa3e440"
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
