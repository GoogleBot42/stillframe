{ nixpkgs, system, service }:

with import (nixpkgs + "/nixos/lib/testing-python.nix") { inherit system; };
simpleTest {
  name = "dynamic-frame-server";
  nodes.machine = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    # The VM has no network, so curl has to be part of its closure.
    environment.systemPackages = [ pkgs.curl ];

    services.picture-frame-server = {
      enable = true;
      # Deliberately a directory the server does not control: /tmp on a NixOS
      # machine also contains systemd-private-* subdirectories, so this is the
      # realistic "imgDir has no usable image in it" case that /fetchImage must
      # survive rather than die on.
      imgDir = "/tmp";
    };
  };

  testScript =
    ''
      # A minimal but valid capability body: 8x8 (packed 2 pixels/byte -> 32
      # bytes) with a two-entry black/white color space.
      REQUEST = (
          '{"width":8,"height":8,"flip_vertical":false,"flip_horizonal":false,'
          '"color_space":[{"color_code":0,"rgb_color":[0,0,0]},'
          '{"color_code":1,"rgb_color":[1,1,1]}]}'
      )
      PORT = 18450

      def post(path, out):
          """POST the capability body and return the HTTP status as a string."""
          return machine.succeed(
              "curl -sS --max-time 30 -o {} -w '%{{http_code}}' ".format(out)
              + "-X POST -H 'Content-Type: application/json' "
              + "--data-binary @/tmp/request.json "
              + "http://127.0.0.1:{}{}".format(PORT, path)
          ).strip()


      machine.start()
      machine.wait_for_unit("multi-user.target")
      machine.wait_for_unit("picture-frame-server")
      machine.wait_for_open_port(PORT)

      machine.succeed(
          "cat > /tmp/request.json <<'JSON'\n" + REQUEST + "\nJSON"
      )

      # /calibrationImage does not touch imgDir, so it must always answer with a
      # real image: 200 and a non-empty body.
      with subtest("calibrationImage returns an image"):
          status = post("/calibrationImage", "/tmp/calibration.bin")
          assert status == "200", "calibrationImage returned HTTP {}".format(status)
          size = int(machine.succeed("stat -c %s /tmp/calibration.bin").strip())
          assert size > 0, "calibrationImage returned an empty body"

      # /fetchImage over an imgDir with nothing usable in it may legitimately
      # fail the request, but it must not take the daemon down with it. Compare
      # the main PID rather than just `is-active`, because Restart=on-failure
      # would otherwise paper over a crash.
      with subtest("fetchImage cannot kill the server"):
          pid_before = machine.succeed(
              "systemctl show -p MainPID --value picture-frame-server"
          ).strip()
          machine.execute(
              "curl -sS --max-time 30 -o /dev/null "
              "-X POST -H 'Content-Type: application/json' "
              "--data-binary @/tmp/request.json "
              "http://127.0.0.1:{}/fetchImage".format(PORT)
          )
          machine.succeed("systemctl is-active picture-frame-server")
          pid_after = machine.succeed(
              "systemctl show -p MainPID --value picture-frame-server"
          ).strip()
          assert pid_before == pid_after, (
              "picture-frame-server restarted during /fetchImage "
              "(pid {} -> {}): the request crashed the daemon".format(
                  pid_before, pid_after
              )
          )

          # Still serving afterwards.
          status = post("/calibrationImage", "/tmp/calibration2.bin")
          assert status == "200", (
              "calibrationImage returned HTTP {} after /fetchImage".format(status)
          )
    '';
}
