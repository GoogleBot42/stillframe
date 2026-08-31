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
    inherit (final.callPackage ./server { }) server;

    # `smartcrop` used to be the Python crop helper the server shelled out to.
    # The aliases below exist for downstream flakes this repo cannot see, and
    # for those a missing attribute is an unexplained eval failure — so leave a
    # stub that says what happened. Nix is lazy, so it costs nothing until
    # somebody actually asks for it. (Deliberately not re-exported from
    # flake.nix's `packages`: that output is forced by `nix flake show` and
    # `nix flake check`, which would turn this into a hard failure for us.)
    smartcrop = throw "stillframe: the smartcrop package was removed; the cropper is now in-process in the server binary";
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
