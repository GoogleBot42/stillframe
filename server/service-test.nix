{ nixpkgs, system, service }:

with import (nixpkgs + "/nixos/lib/testing-python.nix") { inherit system; };
simpleTest {
  name = "dynamic-frame-server";
  nodes.machine = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    services.picture-frame-server = {
      enable = true;
      imgDir = "/tmp";
    };
  };

  testScript =
    ''
      machine.start()
      machine.wait_for_unit("multi-user.target")
      machine.wait_for_unit("picture-frame-server")
    '';
}
