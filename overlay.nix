# Package overlay for the DynamicFrame server.
#
# This file is the single source of truth: flake.nix sets
# `overlays.default = import ./overlay.nix`, so every package, check and the
# NixOS module go through it and `nix flake check` keeps it honest. (It used to
# be an unreferenced copy of an inline overlay in flake.nix, and the two had
# already drifted apart under different attribute names.)
final: prev:
let
  packages = {
    inherit (final.callPackage ./server { }) server smartcrop;
  };
in
{
  # The project answers to both names: this overlay has always exported
  # `dynamic-frame`, while flake.nix's inline overlay and server/service.nix
  # use `picture-frame`. Export the same package set under both attributes so
  # neither this repo nor any unknown downstream consumer breaks.
  dynamic-frame = packages;
  picture-frame = packages;
}
