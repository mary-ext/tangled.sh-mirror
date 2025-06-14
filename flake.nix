{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    indigo = {
      url = "github:oppiliappan/indigo";
      flake = false;
    };
    htmx-src = {
      url = "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js";
      flake = false;
    };
    lucide-src = {
      url = "https://github.com/lucide-icons/lucide/releases/download/0.483.0/lucide-icons-0.483.0.zip";
      flake = false;
    };
    inter-fonts-src = {
      url = "https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip";
      flake = false;
    };
    ibm-plex-mono-src = {
      url = "https://github.com/IBM/plex/releases/download/%40ibm%2Fplex-mono%401.1.0/ibm-plex-mono.zip";
      flake = false;
    };
    sqlite-lib-src = {
      url = "https://sqlite.org/2024/sqlite-amalgamation-3450100.zip";
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
    lucide-src,
    gitignore,
    inter-fonts-src,
    sqlite-lib-src,
    ibm-plex-mono-src,
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
    overlays.default = final: prev: let
      goModHash = "sha256-PJd2UvQJ7HVp2+s+QZPwL2FcdbSsRDxQZHV9hGotB2Y=";
      appviewDeps = {
        inherit htmx-src lucide-src inter-fonts-src ibm-plex-mono-src goModHash gitignoreSource;
      };
      knotDeps = {
        inherit goModHash gitignoreSource;
      };
    in {
      lexgen = final.callPackage ./nix/pkgs/lexgen.nix {inherit indigo;};
      appview = final.pkgsStatic.callPackage ./nix/pkgs/appview.nix appviewDeps;
      appview-cross = final.pkgsCross.gnu64.pkgsStatic.callPackage ./nix/pkgs/appview.nix appviewDeps;
      knot = final.pkgsStatic.callPackage ./nix/pkgs/knot.nix {};
      knot-unwrapped = final.pkgsStatic.callPackage ./nix/pkgs/knot-unwrapped.nix knotDeps;
      knot-cross = final.pkgsCross.gnu64.pkgsStatic.callPackage ./nix/pkgs/knot.nix knotDeps;
      sqlite-lib = final.pkgsStatic.callPackage ./nix/pkgs/sqlite-lib.nix {
        inherit (final.pkgsStatic) gcc;
        inherit sqlite-lib-src;
      };
      genjwks = final.callPackage ./nix/pkgs/genjwks.nix {inherit goModHash gitignoreSource;};
    };

    packages = forAllSystems (system: {
      inherit
        (nixpkgsFor."${system}")
        lexgen
        appview
        appview-cross
        knot
        knot-cross
        knot-unwrapped
        genjwks
        sqlite-lib
        ;
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
          pkgs.lexgen
          pkgs.litecli
          pkgs.websocat
          pkgs.tailwindcss
          pkgs.nixos-shell
          pkgs.redis
        ];
        shellHook = ''
          mkdir -p appview/pages/static/{fonts,icons}
          cp -f ${htmx-src} appview/pages/static/htmx.min.js
          cp -rf ${lucide-src}/*.svg appview/pages/static/icons/
          cp -f ${inter-fonts-src}/web/InterVariable*.woff2 appview/pages/static/fonts/
          cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 appview/pages/static/fonts/
          cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono-Regular.woff2 appview/pages/static/fonts/
          export TANGLED_OAUTH_JWKS="$(${pkgs.genjwks}/bin/genjwks)"
        '';
        env.CGO_ENABLED = 1;
      };
    });
    apps = forAllSystems (system: let
      pkgs = nixpkgsFor."${system}";
      air-watcher = name: arg:
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.air}/bin/air -c /dev/null \
          -build.cmd "${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" \
          -build.bin "./out/${name}.out ${arg}" \
          -build.stop_on_error "true" \
          -build.include_ext "go"
        '';
      tailwind-watcher =
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.tailwindcss}/bin/tailwindcss -w -i input.css -o ./appview/pages/static/tw.css
        '';
    in {
      watch-appview = {
        type = "app";
        program = ''${air-watcher "appview" ""}/bin/run'';
      };
      watch-knot = {
        type = "app";
        program = ''${air-watcher "knot" "server"}/bin/run'';
      };
      watch-tailwind = {
        type = "app";
        program = ''${tailwind-watcher}/bin/run'';
      };
    });

    nixosModules.appview = import ./nix/modules/appview.nix {inherit self;};
    nixosModules.knot = import ./nix/modules/knot.nix {inherit self;};
    nixosConfigurations.knotVM = import ./nix/vm.nix {inherit self nixpkgs;};
  };
}
