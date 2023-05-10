{
  description = "A simple Go project built with Nix";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-22.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        server = pkgs.callPackage ./server/default.nix { };
      in
      {
        packages."dynamic-frame-server" = server;
        packages.default = server;
        devShell = pkgs.mkShell {
          buildInputs = [ pkgs.go ];
        };
      }
    );
}
