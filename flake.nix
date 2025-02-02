{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs";
    indigo = {
      url = "github:oppiliappan/indigo";
      flake = false;
    };
  };

  outputs = {
    self,
    nixpkgs,
    indigo,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system:
      import nixpkgs {
        inherit system;
        overlays = [self.overlays.default];
      });
  in {
    overlays.default = final: prev: {
      indigo-lexgen = with final;
        final.buildGoModule {
          pname = "indigo-lexgen";
          version = "0.1.0";
          src = indigo;
          subPackage = ["cmd/lexgen"];
          vendorHash = null;
          doCheck = false;
        };
    };
    packages = forAllSystems (system: {
      inherit (nixpkgsFor."${system}") indigo-lexgen;
    });
    defaultPackage = forAllSystems (system: nixpkgsFor.${system}.indigo-lexgen);
    formatter = forAllSystems (system: nixpkgsFor."${system}".alejandra);
    devShells = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
    in {
      default = pkgs.mkShell {
        nativeBuildInputs = [
          pkgs.go
          pkgs.air
          pkgs.templ
          pkgs.gopls
          pkgs.httpie
          pkgs.indigo-lexgen
          pkgs.litecli
          pkgs.websocat
        ];
      };
    });
    apps = forAllSystems (system: 
        let
            pkgs = nixpkgsFor."${system}";
            air-watcher = name: pkgs.writeShellScriptBin "run"
            ''
                ${pkgs.air}/bin/air -c /dev/null -build.cmd "${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" -build.bin "./out/${name}.out"
            '';
        in
    {
        watch-appview = {
            type = "app";
            program = ''${air-watcher "appview"}/bin/run'';
        };
        watch-knotserver = {
            type = "app";
            program = ''${air-watcher "knotserver"}/bin/run'';
        };
    });
  };
}
