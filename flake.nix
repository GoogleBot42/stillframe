{
  description = "A simple Go project built with Nix";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/master";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        server = pkgs.callPackage ./server { };
        firmware = pkgs.callPackage ./firmware { };
      in
      {
        packages = {
          inherit (server) server smartcrop;
          inherit firmware;
        };
        packages.default = server.server;
        devShell = pkgs.callPackage ./shell.nix { };
      }
    );
}
