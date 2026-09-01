{ nixpkgs, system, service }:

with import (nixpkgs + "/nixos/lib/testing-python.nix") { inherit system; };
simpleTest {
  name = "stillframe-server";
  nodes.machine = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    # The VM has no network, so curl has to be part of its closure.
    environment.systemPackages = [ pkgs.curl ];

    services.stillframe-server = {
      enable = true;
      # Deliberately a directory the server does not control: /tmp on a NixOS
      # machine also contains systemd-private-* subdirectories, so this is the
      # realistic "imgDir has no usable image in it" case that /fetchImage must
      # survive rather than die on.
      imgDir = "/tmp";
    };
  };

  # The other half of the option: no imgDir at all, which is what an
  # Immich-only (or, as here, source-less) deployment looks like. It has to
  # evaluate, start, and answer requests exactly like the node above.
  nodes.imgless = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    environment.systemPackages = [ pkgs.curl ];

    services.stillframe-server.enable = true;
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

      def post(node, path, out):
          """POST the capability body and return the HTTP status as a string."""
          return node.succeed(
              "curl -sS --max-time 30 -o {} -w '%{{http_code}}' ".format(out)
              + "-X POST -H 'Content-Type: application/json' "
              + "--data-binary @/tmp/request.json "
              + "http://127.0.0.1:{}{}".format(PORT, path)
          ).strip()


      def exec_start(node):
          """The unit's single ExecStart= line, read back from the unit file."""
          unit = node.succeed("systemctl cat stillframe-server")
          lines = [l for l in unit.splitlines() if l.startswith("ExecStart=")]
          assert len(lines) == 1, "expected one ExecStart=, got {}".format(lines)
          return lines[0]


      def check_serves(node, name):
          """The behaviour both configurations owe us, whatever imgDir is."""
          node.succeed(
              "cat > /tmp/request.json <<'JSON'\n" + REQUEST + "\nJSON"
          )

          # /calibrationImage does not touch imgDir, so it must always answer
          # with a real image: 200 and a non-empty body.
          with subtest("calibrationImage returns an image ({})".format(name)):
              status = post(node, "/calibrationImage", "/tmp/calibration.bin")
              assert status == "200", "calibrationImage returned HTTP {}".format(status)
              size = int(node.succeed("stat -c %s /tmp/calibration.bin").strip())
              assert size > 0, "calibrationImage returned an empty body"

          # /fetchImage over an imgDir with nothing usable in it may legitimately
          # fail the request, but it must not take the daemon down with it.
          # Compare the main PID rather than just `is-active`, because
          # Restart=on-failure would otherwise paper over a crash.
          with subtest("fetchImage cannot kill the server ({})".format(name)):
              pid_before = node.succeed(
                  "systemctl show -p MainPID --value stillframe-server"
              ).strip()
              node.execute(
                  "curl -sS --max-time 30 -o /dev/null "
                  "-X POST -H 'Content-Type: application/json' "
                  "--data-binary @/tmp/request.json "
                  "http://127.0.0.1:{}/fetchImage".format(PORT)
              )
              node.succeed("systemctl is-active stillframe-server")
              pid_after = node.succeed(
                  "systemctl show -p MainPID --value stillframe-server"
              ).strip()
              assert pid_before == pid_after, (
                  "stillframe-server restarted during /fetchImage "
                  "(pid {} -> {}): the request crashed the daemon".format(
                      pid_before, pid_after
                  )
              )

              # Still serving afterwards.
              status = post(node, "/calibrationImage", "/tmp/calibration2.bin")
              assert status == "200", (
                  "calibrationImage returned HTTP {} after /fetchImage".format(status)
              )


      start_all()

      for node in (machine, imgless):
          node.wait_for_unit("multi-user.target")
          node.wait_for_unit("stillframe-server")
          node.wait_for_open_port(PORT)

      check_serves(machine, "imgDir set")
      check_serves(imgless, "imgDir unset")

      # An operator-supplied imgDir must leave the unit exactly as it was before
      # imgDir became optional: served straight from that path, with no state
      # directory conjured up behind their back.
      with subtest("imgDir set leaves the unit untouched"):
          line = exec_start(machine)
          assert line.endswith("/bin/server 18450 /tmp"), (
              "unexpected ExecStart: {}".format(line)
          )
          state = machine.succeed(
              "systemctl show -p StateDirectory --value stillframe-server"
          ).strip()
          assert state == "", "unexpected StateDirectory {!r}".format(state)

      # With imgDir unset the server is pointed at a directory systemd owns and
      # creates for it, so the daemon comes up with a readable (if empty) image
      # source rather than a path that does not exist.
      with subtest("imgDir unset gets a state directory"):
          line = exec_start(imgless)
          assert line.endswith("/bin/server 18450 /var/lib/stillframe-server/images"), (
              "unexpected ExecStart: {}".format(line)
          )
          state = imgless.succeed(
              "systemctl show -p StateDirectory --value stillframe-server"
          ).strip()
          assert state == "stillframe-server/images", (
              "unexpected StateDirectory {!r}".format(state)
          )
          imgless.succeed("test -d /var/lib/stillframe-server/images")
    '';
}
