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
        ];
      };
    });
  };
}
