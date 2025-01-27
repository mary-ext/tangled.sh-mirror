{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs";

  };

  outputs = {
    self,
    nixpkgs,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system:
      import nixpkgs {
        inherit system;
      });
  in {
    defaultPackage = forAllSystems (system: self.packages.${system}.legit);
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
        ];
      };
    });
  };
}
