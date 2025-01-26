{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs";

    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    rust-overlay = {
      url = "github:oxalica/rust-overlay";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    self,
    nixpkgs,
    gitignore,
    rust-overlay,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system:
      import nixpkgs {
        inherit system;
        overlays = [(import rust-overlay)];
      });
  in {
    defaultPackage = forAllSystems (system: self.packages.${system}.legit);
    formatter = forAllSystems (system: nixpkgsFor."${system}".alejandra);
    devShells = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
      rust-bin = pkgs.rust-bin.fromRustupToolchainFile ./rust-toolchain.toml;
    in {
      default = pkgs.mkShell {
        nativeBuildInputs = [
          pkgs.go
          pkgs.air

          pkgs.httpie
          pkgs.bacon
          rust-bin
          pkgs.pkg-config
          pkgs.openssl
        ];
        RUST_LOG = "info";
        RUST_BACKTRACE = 1;
      };
    });
  };
}
