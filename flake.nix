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
      goModHash = "sha256-k+WeNx9jZ5YGgskCJYiU2mwyz25E0bhFgSg2GDWZXFw=";
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
          nativeBuildInputs = [ final.makeWrapper ];
          subPackages = ["cmd/knotserver"];
          vendorHash = goModHash;
          installPhase = ''
              runHook preInstall

              mkdir -p $out/bin
              cp $GOPATH/bin/knotserver $out/bin/knotserver

              wrapProgram $out/bin/knotserver \
              --prefix PATH : ${pkgs.git}/bin

              runHook postInstall
          '';
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
          pkgs.nixos-shell
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

    nixosModules.appview = {
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

    nixosModules.knotserver = {
      config,
      pkgs,
      lib,
      ...
    }:
      with lib; {
        options = {
          services.tangled-knotserver = {
            enable = mkOption {
              type = types.bool;
              default = false;
              description = "Enable a tangled knotserver";
            };

            appviewEndpoint = mkOption {
              type = types.str;
              default = "https://tangled.sh";
              description = "Appview endpoint";
            };

            gitUser = mkOption {
              type = types.str;
              default = "git";
              description = "User that hosts git repos and performs git operations";
            };

            repo = {
              scanPath = mkOption {
                type = types.path;
                default = "/home/git";
                description = "Path where repositories are scanned from";
              };

              mainBranch = mkOption {
                type = types.str;
                default = "main";
                description = "Default branch name for repositories";
              };
            };

            server = {
              listenAddr = mkOption {
                type = types.str;
                default = "0.0.0.0:5555";
                description = "Address to listen on";
              };

              internalListenAddr = mkOption {
                type = types.str;
                default = "127.0.0.1:5444";
                description = "Internal address for inter-service communication";
              };

              secret = mkOption {
                type = types.str;
                example = "super-secret-key";
                description = "Secret key provided by appview (required)";
              };

              dbPath = mkOption {
                type = types.path;
                default = "knotserver.db";
                description = "Path to the database file";
              };

              hostname = mkOption {
                type = types.str;
                example = "knot.tangled.sh";
                description = "Hostname for the server (required)";
              };

              dev = mkOption {
                type = types.bool;
                default = false;
                description = "Enable development mode (disables signature verification)";
              };
            };
          };
        };

        config = mkIf config.services.tangled-knotserver.enable {
          nixpkgs.overlays = [self.overlays.default];

          environment.systemPackages = with pkgs; [ git ];

          users.users.git = {
            isNormalUser = true;
            home = "/home/git";
            createHome = true;
            uid = 1000;
            group = "git";
          };

          users.groups.git = {};

          services.openssh = {
            enable = true;
            extraConfig = ''
              Match User git
                  AuthorizedKeysCommand /etc/ssh/keyfetch_wrapper
                  AuthorizedKeysCommandUser nobody
            '';
          };

          environment.etc."ssh/keyfetch_wrapper" = {
              mode = "0555";
              text = ''
                  #!${pkgs.stdenv.shell}
                  ${pkgs.keyfetch}/bin/keyfetch -repoguard-path ${pkgs.repoguard}/bin/repoguard -log-path /tmp/repoguard.log
              '';
          };

          systemd.services.knotserver = {
            description = "knotserver service";
            after = ["network.target" "sshd.service"];
            wantedBy = ["multi-user.target"];
            serviceConfig = {
              User = "git";
              WorkingDirectory = "/home/git";
              Environment = [
                "KNOT_REPO_SCAN_PATH=${config.services.tangled-knotserver.repo.scanPath}"
                "APPVIEW_ENDPOINT=${config.services.tangled-knotserver.appviewEndpoint}"
                "KNOT_SERVER_INTERNAL_LISTEN_ADDR=${config.services.tangled-knotserver.server.internalListenAddr}"
                "KNOT_SERVER_LISTEN_ADDR=${config.services.tangled-knotserver.server.listenAddr}"
                "KNOT_SERVER_SECRET=${config.services.tangled-knotserver.server.secret}"
                "KNOT_SERVER_HOSTNAME=${config.services.tangled-knotserver.server.hostname}"
              ];
              ExecStart = "${pkgs.knotserver}/bin/knotserver";
              Restart = "always";
            };
          };

          networking.firewall.allowedTCPPorts = [22];
        };
      };

    nixosConfigurations.knotVM = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        self.nixosModules.knotserver
        ({
          config,
          pkgs,
          ...
        }: {
          virtualisation.memorySize = 2048;
          virtualisation.cores = 2;
          services.getty.autologinUser = "root";
          environment.systemPackages = with pkgs; [curl vim git];
          services.tangled-knotserver = {
            enable = true;
            server = {
              secret = "ad7b32ded52fbe96e09f469a288084ee01cd12c971da87a1cbb87ef67081bd87";
              hostname = "localhost:6000";
              listenAddr = "0.0.0.0:6000";
            };
          };
        })
      ];
    };
  };
}
