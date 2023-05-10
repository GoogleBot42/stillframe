{ pkgs, ... }:

pkgs.buildGoModule rec {
  pname = "dynamic-frame-server";
  version = "0.0.1";

  src = ./.;

  vendorSha256 = "uTqGdqswbnmPsEm5NISD0Nh9ry4GLMAsuVF7+U8BRdA=";
}
