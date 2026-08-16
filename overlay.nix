# Package overlay for the Stillframe server.
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
  # `stillframe` is the current name and what this repo (flake.nix,
  # server/service.nix) uses. The project has also answered to `dynamic-frame`
  # (this overlay's original attribute) and `picture-frame` (the repository
  # name, used by flake.nix's old inline overlay); unknown downstream flakes may
  # still refer to either, so keep both as aliases of the same package set.
  stillframe = packages;
  picture-frame = packages; # deprecated alias
  dynamic-frame = packages; # deprecated alias
}
