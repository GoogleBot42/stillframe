{
  description = "A simple Go project built with Nix";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/master";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      supportedSystems = with flake-utils.lib.system; [ x86_64-linux i686-linux aarch64-linux ];
    in
    {
      overlays.default = final: prev: {
        picture-frame = {
          inherit (final.callPackage ./server { }) server smartcrop;
          firmware = final.callPackage ./firmware { };
        };
      };
    }
    //
    flake-utils.lib.eachSystem supportedSystems (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ self.overlays.default ];
        };
      in
      {
        packages = with pkgs.picture-frame; {
          inherit server firmware smartcrop;
          default = server;
        };
        devShell = pkgs.callPackage ./shell.nix { };

        checks.build = pkgs.picture-frame.server;
        checks.service = import ./server/service-test.nix {
          inherit nixpkgs system;
          service = self.nixosModules.service;
        };
      }
    )
    //
    {
      nixosModules.default = self.nixosModules.service;
      nixosModules.service = import ./server/service.nix { overlay = self.overlays.default; };
    };
}
