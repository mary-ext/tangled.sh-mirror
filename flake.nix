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
      goModHash = "sha256-mzM0B0ObAahznsL0JXMkFWN1Oix/ObOErUPH31xUMjM=";
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
      indigo-lexgen = final.buildGoModule {
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
            mkdir -p appview/pages/static/{fonts,icons}
            cp -f ${htmx-src} appview/pages/static/htmx.min.js
            cp -rf ${lucide-src}/*.svg appview/pages/static/icons/
            cp -f ${inter-fonts-src}/web/InterVariable*.woff2 appview/pages/static/fonts/
            cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 appview/pages/static/fonts/
            cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono-Regular.woff2 appview/pages/static/fonts/
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
          nativeBuildInputs = [final.makeWrapper];
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
      knotserver-unwrapped = final.pkgsStatic.buildGoModule {
        pname = "knotserver";
        version = "0.1.0";
        src = gitignoreSource ./.;
        subPackages = ["cmd/knotserver"];
        vendorHash = goModHash;
        env.CGO_ENABLED = 1;
      };
      repoguard = buildCmdPackage "repoguard";
      keyfetch = buildCmdPackage "keyfetch";
      genjwks = buildCmdPackage "genjwks";
    };
    packages = forAllSystems (system: {
      inherit
        (nixpkgsFor."${system}")
        indigo-lexgen
        appview
        knotserver
        knotserver-unwrapped
        repoguard
        keyfetch
        genjwks
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
          pkgs.indigo-lexgen
          pkgs.litecli
          pkgs.websocat
          pkgs.tailwindcss
          pkgs.nixos-shell
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
      air-watcher = name:
        pkgs.writeShellScriptBin "run"
        ''
          ${pkgs.air}/bin/air -c /dev/null \
          -build.cmd "${pkgs.go}/bin/go build -o ./out/${name}.out ./cmd/${name}/main.go" \
          -build.bin "./out/${name}.out" \
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
        program = ''${air-watcher "appview"}/bin/run'';
      };
      watch-knotserver = {
        type = "app";
        program = ''${air-watcher "knotserver"}/bin/run'';
      };
      watch-tailwind = {
        type = "app";
        program = ''${tailwind-watcher}/bin/run'';
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
          systemd.services.tangled-appview = {
            description = "tangled appview service";
            wantedBy = ["multi-user.target"];

            serviceConfig = {
              ListenStream = "0.0.0.0:${toString config.services.tangled-appview.port}";
              ExecStart = "${self.packages.${pkgs.system}.appview}/bin/appview";
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
    }: let
      cfg = config.services.tangled-knotserver;
    in
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

            openFirewall = mkOption {
              type = types.bool;
              default = true;
              description = "Open port 22 in the firewall for ssh";
            };

            stateDir = mkOption {
              type = types.path;
              default = "/home/${cfg.gitUser}";
              description = "Tangled knot data directory";
            };

            repo = {
              scanPath = mkOption {
                type = types.path;
                default = cfg.stateDir;
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

              secretFile = mkOption {
                type = lib.types.path;
                example = "KNOT_SERVER_SECRET=<hash>";
                description = "File containing secret key provided by appview (required)";
              };

              dbPath = mkOption {
                type = types.path;
                default = "${cfg.stateDir}/knotserver.db";
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

        config = mkIf cfg.enable {
          environment.systemPackages = with pkgs; [git];

          system.activationScripts.gitConfig = ''
            mkdir -p "${cfg.repo.scanPath}"
            chown -R ${cfg.gitUser}:${cfg.gitUser} \
                "${cfg.repo.scanPath}"

            mkdir -p "${cfg.stateDir}/.config/git"
            cat > "${cfg.stateDir}/.config/git/config" << EOF
            [user]
                name = Git User
                email = git@example.com
            EOF
            chown -R ${cfg.gitUser}:${cfg.gitUser} \
                "${cfg.stateDir}"
          '';

          users.users.${cfg.gitUser} = {
            isSystemUser = true;
            useDefaultShell = true;
            home = cfg.stateDir;
            createHome = true;
            group = cfg.gitUser;
          };

          users.groups.${cfg.gitUser} = {};

          services.openssh = {
            enable = true;
            extraConfig = ''
              Match User ${cfg.gitUser}
                  AuthorizedKeysCommand /etc/ssh/keyfetch_wrapper
                  AuthorizedKeysCommandUser nobody
            '';
          };

          environment.etc."ssh/keyfetch_wrapper" = {
            mode = "0555";
            text = ''
              #!${pkgs.stdenv.shell}
              ${self.packages.${pkgs.system}.keyfetch}/bin/keyfetch \
                -repoguard-path ${self.packages.${pkgs.system}.repoguard}/bin/repoguard \
                -internal-api "http://${cfg.server.internalListenAddr}" \
                -git-dir "${cfg.repo.scanPath}" \
                -log-path /tmp/repoguard.log
            '';
          };

          systemd.services.knotserver = {
            description = "knotserver service";
            after = ["network.target" "sshd.service"];
            wantedBy = ["multi-user.target"];
            serviceConfig = {
              User = cfg.gitUser;
              WorkingDirectory = cfg.stateDir;
              Environment = [
                "KNOT_REPO_SCAN_PATH=${cfg.repo.scanPath}"
                "KNOT_REPO_MAIN_BRANCH=${cfg.repo.mainBranch}"
                "APPVIEW_ENDPOINT=${cfg.appviewEndpoint}"
                "KNOT_SERVER_INTERNAL_LISTEN_ADDR=${cfg.server.internalListenAddr}"
                "KNOT_SERVER_LISTEN_ADDR=${cfg.server.listenAddr}"
                "KNOT_SERVER_DB_PATH=${cfg.server.dbPath}"
                "KNOT_SERVER_HOSTNAME=${cfg.server.hostname}"
              ];
              EnvironmentFile = cfg.server.secretFile;
              ExecStart = "${self.packages.${pkgs.system}.knotserver}/bin/knotserver";
              Restart = "always";
            };
          };

          networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [22];
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
          virtualisation.diskSize = 10 * 1024;
          virtualisation.cores = 2;
          services.getty.autologinUser = "root";
          environment.systemPackages = with pkgs; [curl vim git];
          systemd.tmpfiles.rules = let
            u = config.services.tangled-knotserver.gitUser;
            g = config.services.tangled-knotserver.gitUser;
          in [
            "d /var/lib/knotserver 0770 ${u} ${g} - -" # Create the directory first
            "f+ /var/lib/knotserver/secret 0660 ${u} ${g} - KNOT_SERVER_SECRET=38a7c3237c2a585807e06a5bcfac92eb39442063f3da306b7acb15cfdc51d19d"
          ];
          services.tangled-knotserver = {
            enable = true;
            server = {
              secretFile = "/var/lib/knotserver/secret";
              hostname = "localhost:6000";
              listenAddr = "0.0.0.0:6000";
            };
          };
        })
      ];
    };
  };
}
