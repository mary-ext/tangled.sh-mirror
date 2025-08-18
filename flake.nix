{
  description = "atproto github";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    indigo = {
      url = "github:oppiliappan/indigo";
      flake = false;
    };
    htmx-src = {
      url = "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js";
      flake = false;
    };
    htmx-ws-src = {
      # strange errors in consle that i can't really make out
      # url = "https://unpkg.com/htmx.org@2.0.4/dist/ext/ws.js";
      url = "https://cdn.jsdelivr.net/npm/htmx-ext-ws@2.0.2";
      flake = false;
    };
    lucide-src = {
      url = "https://github.com/lucide-icons/lucide/releases/download/0.536.0/lucide-icons-0.536.0.zip";
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
  };

  outputs = {
    self,
    nixpkgs,
    gomod2nix,
    indigo,
    htmx-src,
    htmx-ws-src,
    lucide-src,
    inter-fonts-src,
    sqlite-lib-src,
    ibm-plex-mono-src,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system: nixpkgs.legacyPackages.${system});

    mkPackageSet = pkgs:
      pkgs.lib.makeScope pkgs.newScope (self: {
        src = let
          fs = pkgs.lib.fileset;
        in
          fs.toSource {
            root = ./.;
            fileset = fs.difference (fs.intersection (fs.gitTracked ./.) (fs.fileFilter (file: !(file.hasExt "nix")) ./.)) (fs.maybeMissing ./.jj);
          };
        buildGoApplication =
          (self.callPackage "${gomod2nix}/builder" {
            gomod2nix = gomod2nix.legacyPackages.${pkgs.system}.gomod2nix;
          }).buildGoApplication;
        modules = ./nix/gomod2nix.toml;
        sqlite-lib = self.callPackage ./nix/pkgs/sqlite-lib.nix {
          inherit (pkgs) gcc;
          inherit sqlite-lib-src;
        };
        genjwks = self.callPackage ./nix/pkgs/genjwks.nix {};
        lexgen = self.callPackage ./nix/pkgs/lexgen.nix {inherit indigo;};
        appview-static-files = self.callPackage ./nix/pkgs/appview-static-files.nix {
          inherit htmx-src htmx-ws-src lucide-src inter-fonts-src ibm-plex-mono-src;
        };
        appview = self.callPackage ./nix/pkgs/appview.nix {};
        spindle = self.callPackage ./nix/pkgs/spindle.nix {};
        knot-unwrapped = self.callPackage ./nix/pkgs/knot-unwrapped.nix {};
        knot = self.callPackage ./nix/pkgs/knot.nix {};
      });
  in {
    overlays.default = final: prev: {
      inherit (mkPackageSet final) lexgen sqlite-lib genjwks spindle knot-unwrapped knot appview;
    };

    packages = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
      packages = mkPackageSet pkgs;
      staticPackages = mkPackageSet pkgs.pkgsStatic;
      crossPackages = mkPackageSet pkgs.pkgsCross.gnu64.pkgsStatic;
    in {
      inherit (packages) appview appview-static-files lexgen genjwks spindle knot knot-unwrapped sqlite-lib;

      pkgsStatic-appview = staticPackages.appview;
      pkgsStatic-knot = staticPackages.knot;
      pkgsStatic-knot-unwrapped = staticPackages.knot-unwrapped;
      pkgsStatic-spindle = staticPackages.spindle;
      pkgsStatic-sqlite-lib = staticPackages.sqlite-lib;

      pkgsCross-gnu64-pkgsStatic-appview = crossPackages.appview;
      pkgsCross-gnu64-pkgsStatic-knot = crossPackages.knot;
      pkgsCross-gnu64-pkgsStatic-knot-unwrapped = crossPackages.knot-unwrapped;
      pkgsCross-gnu64-pkgsStatic-spindle = crossPackages.spindle;

      treefmt-wrapper = pkgs.treefmt.withConfig {
        settings.formatter = {
          alejandra = {
            command = pkgs.lib.getExe pkgs.alejandra;
            includes = ["*.nix"];
          };

          gofmt = {
            command = pkgs.lib.getExe' pkgs.go "gofmt";
            options = ["-w"];
            includes = ["*.go"];
          };

          # prettier = let
          #   wrapper = pkgs.runCommandLocal "prettier-wrapper" {nativeBuildInputs = [pkgs.makeWrapper];} ''
          #     makeWrapper ${pkgs.prettier}/bin/prettier "$out" --add-flags "--plugin=${pkgs.prettier-plugin-go-template}/lib/node_modules/prettier-plugin-go-template/lib/index.js"
          #   '';
          # in {
          #   command = wrapper;
          #   options = ["-w"];
          #   includes = ["*.html"];
          #   # causes Go template plugin errors: https://github.com/NiklasPor/prettier-plugin-go-template/issues/120
          #   excludes = ["appview/pages/templates/layouts/repobase.html" "appview/pages/templates/repo/tags.html"];
          # };
        };
      };
    });
    defaultPackage = forAllSystems (system: self.packages.${system}.appview);
    devShells = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
      packages' = self.packages.${system};
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
          pkgs.litecli
          pkgs.websocat
          pkgs.tailwindcss
          pkgs.nixos-shell
          pkgs.redis
          pkgs.coreutils # for those of us who are on systems that use busybox (alpine)
          packages'.lexgen
          packages'.treefmt-wrapper
        ];
        shellHook = ''
          mkdir -p appview/pages/static
          # no preserve is needed because watch-tailwind will want to be able to overwrite
          cp -fr --no-preserve=ownership ${packages'.appview-static-files}/* appview/pages/static
          export TANGLED_OAUTH_JWKS="$(${packages'.genjwks}/bin/genjwks)"
        '';
        env.CGO_ENABLED = 1;
      };
    });
    apps = forAllSystems (system: let
      pkgs = nixpkgsFor."${system}";
      packages' = self.packages.${system};
      air-watcher = name: arg:
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.air}/bin/air -c /dev/null \
          -build.cmd "${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" \
          -build.bin "./out/${name}.out" \
          -build.args_bin "${arg}" \
          -build.stop_on_error "true" \
          -build.include_ext "go"
        '';
      tailwind-watcher =
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.tailwindcss}/bin/tailwindcss -w -i input.css -o ./appview/pages/static/tw.css
        '';
    in {
      fmt = {
        type = "app";
        program = pkgs.lib.getExe packages'.treefmt-wrapper;
      };
      watch-appview = {
        type = "app";
        program = toString (pkgs.writeShellScript "watch-appview" ''
          echo "copying static files to appview/pages/static..."
          ${pkgs.coreutils}/bin/cp -fr --no-preserve=ownership ${packages'.appview-static-files}/* appview/pages/static
          ${air-watcher "appview" ""}/bin/run
        '');
      };
      watch-knot = {
        type = "app";
        program = ''${air-watcher "knot" "server"}/bin/run'';
      };
      watch-tailwind = {
        type = "app";
        program = ''${tailwind-watcher}/bin/run'';
      };
      vm = let
        guestSystem =
          if pkgs.stdenv.hostPlatform.isAarch64
          then "aarch64-linux"
          else "x86_64-linux";
      in {
        type = "app";
        program =
          (pkgs.writeShellApplication {
            name = "launch-vm";
            text = ''
              rootDir=$(jj --ignore-working-copy root || git rev-parse --show-toplevel) || (echo "error: can't find repo root?"; exit 1)
              cd "$rootDir"

              mkdir -p nix/vm-data/{knot,repos,spindle,spindle-logs}

              export TANGLED_VM_DATA_DIR="$rootDir/nix/vm-data"
              exec ${pkgs.lib.getExe
                (import ./nix/vm.nix {
                  inherit nixpkgs self;
                  system = guestSystem;
                  hostSystem = system;
                }).config.system.build.vm}
            '';
          })
          + /bin/launch-vm;
      };
      gomod2nix = {
        type = "app";
        program = toString (pkgs.writeShellScript "gomod2nix" ''
          ${gomod2nix.legacyPackages.${system}.gomod2nix}/bin/gomod2nix generate --outdir ./nix
        '');
      };
      lexgen = {
        type = "app";
        program =
          (pkgs.writeShellApplication {
            name = "lexgen";
            text = ''
              if ! command -v lexgen > /dev/null; then
                echo "error: must be executed from devshell"
                exit 1
              fi

              rootDir=$(jj --ignore-working-copy root || git rev-parse --show-toplevel) || (echo "error: can't find repo root?"; exit 1)
              cd "$rootDir"

              rm -f api/tangled/*
              lexgen --build-file lexicon-build-config.json lexicons
              sed -i.bak 's/\tutil/\/\/\tutil/' api/tangled/*
              ${pkgs.gotools}/bin/goimports -w api/tangled/*
              go run cmd/gen.go
              lexgen --build-file lexicon-build-config.json lexicons
              rm api/tangled/*.bak
            '';
          })
          + /bin/lexgen;
      };
    });

    nixosModules.appview = {
      lib,
      pkgs,
      ...
    }: {
      imports = [./nix/modules/appview.nix];

      services.tangled-appview.package = lib.mkDefault self.packages.${pkgs.system}.appview;
    };
    nixosModules.knot = {
      lib,
      pkgs,
      ...
    }: {
      imports = [./nix/modules/knot.nix];

      services.tangled-knot.package = lib.mkDefault self.packages.${pkgs.system}.knot;
    };
    nixosModules.spindle = {
      lib,
      pkgs,
      ...
    }: {
      imports = [./nix/modules/spindle.nix];

      services.tangled-spindle.package = lib.mkDefault self.packages.${pkgs.system}.spindle;
    };
  };
}
