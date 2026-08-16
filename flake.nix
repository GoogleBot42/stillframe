{
  description = "A simple Go project built with Nix";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      supportedSystems = with flake-utils.lib.system; [ x86_64-linux i686-linux aarch64-linux ];
    in
    {
      # Single source of truth for the package set — see overlay.nix. Everything
      # below (packages, checks, the NixOS module) goes through this overlay, so
      # `nix flake check` also evaluates the file downstream flakes import.
      overlays.default = import ./overlay.nix;
    }
    //
    flake-utils.lib.eachSystem supportedSystems (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ self.overlays.default ];
        };
        inherit (pkgs) lib;
      in
      {
        packages = with pkgs.picture-frame; {
          inherit server smartcrop;
          default = server;
        };
        devShells.default = pkgs.callPackage ./shell.nix { };

        checks.build = pkgs.picture-frame.server;
        checks.service = import ./server/service-test.nix {
          inherit nixpkgs system;
          service = self.nixosModules.service;
        };

        # Host unit tests for the panel-independent display logic
        # (esphome/tests/run.sh). Compiling and running them here is what makes
        # them run automatically — nothing else in the tree does.
        checks.einkTests = pkgs.stdenv.mkDerivation {
          name = "eink-host-tests";

          # run.sh derives repo_root from its own location and includes
          # `$repo_root` plus esphome/components/eink_frame/eink_frame.cpp, so
          # the sources have to keep their repo-relative layout. Restricted to
          # the C++/shell files so unrelated edits (and __pycache__) do not
          # rebuild the check.
          src = lib.fileset.toSource {
            root = ./.;
            fileset = lib.fileset.intersection
              (lib.fileset.unions [ ./esphome/tests ./esphome/components ])
              (lib.fileset.fileFilter (f: f.hasExt "cpp" || f.hasExt "h" || f.hasExt "sh") ./esphome);
          };

          dontConfigure = true;

          buildPhase = ''
            runHook preBuild
            # Point run.sh at the compiler from this derivation's stdenv, and
            # set EINK_TESTS_NIX_RETRY so its `nix shell nixpkgs#gcc` fallback
            # can never fire (there is no network or nix daemon in the sandbox).
            export CXX=${pkgs.stdenv.cc.targetPrefix}c++
            export EINK_TESTS_NIX_RETRY=1
            bash esphome/tests/run.sh
            runHook postBuild
          '';

          # Reached only if run.sh exited 0 (it is `set -euo pipefail` and the
          # test binary's own exit status is its last command), so a failing
          # test fails the check.
          installPhase = ''
            runHook preInstall
            touch $out
            runHook postInstall
          '';
        };
      }
    )
    //
    {
      nixosModules.default = self.nixosModules.service;
      nixosModules.service = import ./server/service.nix { overlay = self.overlays.default; };
    };
}
