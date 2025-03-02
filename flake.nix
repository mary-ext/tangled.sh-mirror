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
    lucide-src = {
      url = "https://unpkg.com/lucide@latest";
      flake = false;
    };
    ia-fonts-src = {
      url = "github:iaolo/iA-Fonts";
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
    ia-fonts-src,
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
      goModHash = "sha256-ywhhGrv8KNqy9tCMCnA1PU/RQ/+0Xyitej1L48TcFvI=";
      buildCmdPackage = name:
        final.buildGoModule {
          pname = name;
          version = "0.1.0";
          src = gitignoreSource ./.;
          subPackages = ["cmd/${name}"];
          vendorHash = goModHash;
          env.CGO_ENABLED = 0;
        };
    in {
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
          postUnpack = ''
            pushd source
            cp -f ${htmx-src} appview/pages/static/htmx.min.js
            cp -f ${lucide-src} appview/pages/static/lucide.min.js
            mkdir -p appview/pages/static/fonts
            cp -f ${ia-fonts-src}/"iA Writer Quattro"/Static/*.ttf appview/pages/static/fonts/
            cp -f ${ia-fonts-src}/"iA Writer Mono"/Static/*.ttf appview/pages/static/fonts/
            ${pkgs.tailwindcss}/bin/tailwindcss -i input.css -o appview/pages/static/tw.css
            popd
          '';
          doCheck = false;
          subPackages = ["cmd/appview"];
          vendorHash = goModHash;
          env.CGO_ENABLED = 1;
          stdenv = pkgsStatic.stdenv;
        };

      knotserver = with final;
        final.pkgsStatic.buildGoModule {
          pname = "knotserver";
          version = "0.1.0";
          src = gitignoreSource ./.;
          subPackages = ["cmd/knotserver"];
          vendorHash = goModHash;
          env.CGO_ENABLED = 1;
        };
      repoguard = buildCmdPackage "repoguard";
      keyfetch = buildCmdPackage "keyfetch";
    };
    packages = forAllSystems (system: {
      inherit (nixpkgsFor."${system}") indigo-lexgen appview knotserver repoguard keyfetch;
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
        shellHook = ''
          cp -f ${htmx-src} appview/pages/static/htmx.min.js
          cp -f ${lucide-src} appview/pages/static/lucide.min.js
          cp -f ${ia-fonts-src}/"iA Writer Quattro"/Static/*.ttf appview/pages/static/fonts/
          cp -f ${ia-fonts-src}/"iA Writer Mono"/Static/*.ttf appview/pages/static/fonts/
        '';
      };
    });
    apps = forAllSystems (system: let
      pkgs = nixpkgsFor."${system}";
      air-watcher = name:
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.air}/bin/air -c /dev/null \
          -build.cmd "${pkgs.tailwindcss}/bin/tailwindcss -i input.css -o ./appview/pages/static/tw.css && ${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" \
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

    nixosModules.default = {
      config,
      pkgs,
      lib,
      ...
    }:
      with lib; {
        options = {
          services.tangled-appview = {
            enable = mkOption {
              type = types.bool;
              default = false;
              description = "Enable tangled appview";
            };
            port = mkOption {
              type = types.int;
              default = 3000;
              description = "Port to run the appview on";
            };
            cookie_secret = mkOption {
              type = types.str;
              default = "00000000000000000000000000000000";
              description = "Cookie secret";
            };
          };
        };

        config = mkIf config.services.tangled-appview.enable {
          nixpkgs.overlays = [self.overlays.default];
          systemd.services.tangled-appview = {
            description = "tangled appview service";
            wantedBy = ["multi-user.target"];

            serviceConfig = {
              ListenStream = "0.0.0.0:${toString config.services.tangled-appview.port}";
              ExecStart = "${pkgs.tangled-appview}/bin/tangled-appview";
              Restart = "always";
            };

            environment = {
              TANGLED_DB_PATH = "appview.db";
              TANGLED_COOKIE_SECRET = config.services.tangled-appview.cookie_secret;
            };
          };
        };
      };
  };
}
