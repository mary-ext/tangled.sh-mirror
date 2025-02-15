{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs";
    indigo = {
      url = "github:oppiliappan/indigo";
      flake = false;
    };
    htmx-src = {
      url = "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js";
      flake = false;
    };
    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    self,
    nixpkgs,
    indigo,
    htmx-src,
    gitignore,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system:
      import nixpkgs {
        inherit system;
        overlays = [self.overlays.default];
      });
    inherit (gitignore.lib) gitignoreSource;
  in {
    overlays.default = final: prev: {
      indigo-lexgen = with final;
        final.buildGoModule {
          pname = "indigo-lexgen";
          version = "0.1.0";
          src = indigo;
          subPackages = ["cmd/lexgen"];
          vendorHash = "sha256-pGc29fgJFq8LP7n/pY1cv6ExZl88PAeFqIbFEhB3xXs=";
          doCheck = false;
        };

      appview = with final;
        final.pkgsStatic.buildGoModule {
          pname = "appview";
          version = "0.1.0";
          src = gitignoreSource ./.;
          postConfigureHook = ''
            cp -f ${htmx-src} appview/pages/static/htmx.min.js
            ${pkgs.tailwindcss}/bin/tailwindcss -i input.css -o appview/pages/static/tw.css
          '';
          subPackages = ["cmd/appview"];
          vendorHash = "sha256-QgUPTOgAdKUTg+ztfs194G7pt3/qDtqTMkDRmMECxSo=";
          env.CGO_ENABLED = 1;
          stdenv = pkgsStatic.stdenv;
        };

        knotserver = with final;
          final.pkgsStatic.buildGoModule {
            pname = "knotserver";
            version = "0.1.0";
            src = gitignoreSource ./.;
            subPackages = ["cmd/knotserver"];
            vendorHash = "sha256-QgUPTOgAdKUTg+ztfs194G7pt3/qDtqTMkDRmMECxSo=";
            env.CGO_ENABLED = 1;
            nativeBuildInputs = with pkgsMusl; [ pkg-config ];

            # Add these ldflags for static compilation
            ldflags = [ "-s" "-w" "-linkmode external" ''-extldflags "-static -L${pkgsStatic.musl}/lib"'' ];

            # Use static stdenv
            stdenv = pkgMusl.stdenv;
          };
    };
    packages = forAllSystems (system: {
      inherit (nixpkgsFor."${system}") indigo-lexgen appview knotserver;
    });
    defaultPackage = forAllSystems (system: nixpkgsFor.${system}.appview);
    formatter = forAllSystems (system: nixpkgsFor."${system}".alejandra);
    devShells = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
      staticShell = pkgs.mkShell.override {
        stdenv = pkgs.pkgsStatic.stdenv;
      };
    in {
      default = staticShell {
        nativeBuildInputs = [
          pkgs.go
          pkgs.air
          pkgs.gopls
          pkgs.httpie
          pkgs.indigo-lexgen
          pkgs.litecli
          pkgs.websocat
          pkgs.tailwindcss
        ];
      };
    });
    apps = forAllSystems (system: let
      pkgs = nixpkgsFor."${system}";
      air-watcher = name:
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.air}/bin/air -c /dev/null \
          -build.cmd "cp -rf ${htmx-src} appview/pages/static/htmx.min.js && ${pkgs.tailwindcss}/bin/tailwindcss -i input.css -o ./appview/pages/static/tw.css && ${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" \
          -build.bin "./out/${name}.out" \
          -build.include_ext "go,html,css"
        '';
    in {
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
