{ pkgs ? import <nixpkgs> { }, ... }:

with pkgs;

mkShell {
  buildInputs = [
    go
    python3
    esphome
  ];

  # Disabling hardening is required for go debugger to work. Not needed for packaging.
  NIX_HARDENING_ENABLE = "";
}
