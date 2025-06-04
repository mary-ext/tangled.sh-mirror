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
      goModHash = "sha256-ZckpIPqFk7/XBiEJUbmrAzdjAxV62hv896xqAXF2aZs=";
      appviewBuildAttrs = final:
        with final; {
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
      knotBuildAttrs = final:
        with final; {
          pname = "knot";
          version = "0.1.0";
          src = gitignoreSource ./.;
          subPackages = ["cmd/knot"];
          vendorHash = goModHash;
          env.CGO_ENABLED = 1;
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

      # appview packages
      appview = final.pkgsStatic.buildGoModule (appviewBuildAttrs final);
      appview-cross = final.pkgsCross.gnu64.pkgsStatic.buildGoModule (appviewBuildAttrs final);

      # knot packages
      knot = with final;
        final.pkgsStatic.buildGoModule ((knotBuildAttrs final)
          // {
            nativeBuildInputs = [final.makeWrapper];
            installPhase = ''
              runHook preInstall

              mkdir -p $out/bin
              cp $GOPATH/bin/knot $out/bin/knot

              wrapProgram $out/bin/knot \
              --prefix PATH : ${pkgs.git}/bin

              runHook postInstall
            '';
          });
      knot-cross = final.pkgsCross.gnu64.pkgsStatic.buildGoModule (knotBuildAttrs final); # cross-compile on darwin to x86_64-linux
      knot-unwrapped = final.pkgsStatic.buildGoModule (knotBuildAttrs final);

      genjwks = final.callPackage ./nix/pkgs/genjwks.nix {inherit goModHash gitignoreSource;};
    };
    packages = forAllSystems (system: {
      inherit
        (nixpkgsFor."${system}")
        indigo-lexgen
        appview
        appview-cross
        knot
        knot-cross
        knot-unwrapped
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
