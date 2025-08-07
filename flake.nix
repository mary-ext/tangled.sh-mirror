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
    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
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
    gitignore,
    inter-fonts-src,
    sqlite-lib-src,
    ibm-plex-mono-src,
  }: let
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    nixpkgsFor = forAllSystems (system: nixpkgs.legacyPackages.${system});

    mkPackageSet = pkgs:
      pkgs.lib.makeScope pkgs.newScope (self: {
        inherit (gitignore.lib) gitignoreSource;
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
        appview = self.callPackage ./nix/pkgs/appview.nix {
          inherit htmx-src htmx-ws-src lucide-src inter-fonts-src ibm-plex-mono-src;
        };
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
      appview = packages.appview;
      lexgen = packages.lexgen;
      knot = packages.knot;
      knot-unwrapped = packages.knot-unwrapped;
      spindle = packages.spindle;
      genjwks = packages.genjwks;
      sqlite-lib = packages.sqlite-lib;

      pkgsStatic-appview = staticPackages.appview;
      pkgsStatic-knot = staticPackages.knot;
      pkgsStatic-knot-unwrapped = staticPackages.knot-unwrapped;
      pkgsStatic-spindle = staticPackages.spindle;
      pkgsStatic-sqlite-lib = staticPackages.sqlite-lib;

      pkgsCross-gnu64-pkgsStatic-appview = crossPackages.appview;
      pkgsCross-gnu64-pkgsStatic-knot = crossPackages.knot;
      pkgsCross-gnu64-pkgsStatic-knot-unwrapped = crossPackages.knot-unwrapped;
      pkgsCross-gnu64-pkgsStatic-spindle = crossPackages.spindle;
    });
    defaultPackage = forAllSystems (system: self.packages.${system}.appview);
    formatter = forAllSystems (system: nixpkgsFor.${system}.alejandra);
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
          packages'.lexgen
        ];
        shellHook = ''
          mkdir -p appview/pages/static/{fonts,icons}
          cp -f ${htmx-src} appview/pages/static/htmx.min.js
          cp -f ${htmx-ws-src} appview/pages/static/htmx-ext-ws.min.js
          cp -rf ${lucide-src}/*.svg appview/pages/static/icons/
          cp -f ${inter-fonts-src}/web/InterVariable*.woff2 appview/pages/static/fonts/
          cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 appview/pages/static/fonts/
          cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono-Regular.woff2 appview/pages/static/fonts/
          export TANGLED_OAUTH_JWKS="$(${packages'.genjwks}/bin/genjwks)"
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
      vm = {
        type = "app";
        program = toString (pkgs.writeShellScript "vm" ''
          ${pkgs.nixos-shell}/bin/nixos-shell --flake .#vm
        '');
      };
      gomod2nix = {
        type = "app";
        program = toString (pkgs.writeShellScript "gomod2nix" ''
          ${gomod2nix.legacyPackages.${system}.gomod2nix}/bin/gomod2nix generate --outdir ./nix
        '');
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
    nixosConfigurations.vm = import ./nix/vm.nix {inherit self nixpkgs;};
  };
}
