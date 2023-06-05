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
        server = pkgs.callPackage ./server/server.nix { };
        smartcrop = pkgs.callPackage ./server/smartcrop.nix { };
      in
      {
        packages."dynamic-frame-server" = server;
        packages."dynamic-frame-smartcrop" = smartcrop;
        packages.default = server;
        devShell = pkgs.callPackage ./shell.nix { };
      }
    );
}
