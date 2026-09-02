{ nixpkgs, system, service }:

with import (nixpkgs + "/nixos/lib/testing-python.nix") { inherit system; };
simpleTest {
  name = "stillframe-server";
  nodes.machine = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    # The VM has no network, so the tools the test script drives it with have
    # to be part of its closure.
    environment.systemPackages = [ pkgs.curl pkgs.iproute2 ];

    # A directory outside the sandbox's writable set, holding one file that is
    # not a picture: the realistic "imgDir has nothing usable in it" case that
    # /fetchImage must survive rather than die on. It is also the case
    # ProtectSystem=strict has to keep readable — the daemon may not write here,
    # but it must be able to walk it.
    #
    # The space in the name is deliberate. Before ExecStart quoted its
    # arguments, systemd split this path in two and the server was handed
    # "/srv/my" as its image directory.
    systemd.tmpfiles.rules = [
      "d '/srv/my photos' 0755 root root -"
      "f '/srv/my photos/notes.txt' 0644 root root - hello"
    ];

    services.stillframe-server = {
      enable = true;
      imgDir = "/srv/my photos";
    };
  };

  # The other half of the option: no imgDir at all, which is what an
  # Immich-only (or, as here, source-less) deployment looks like. It has to
  # evaluate, start, and answer requests exactly like the node above.
  #
  # This node also pins the non-default bindAddress: everything below still has
  # to work when the server is listening on loopback only.
  nodes.imgless = { config, pkgs, ... }: {
    imports = [ service ];

    virtualisation.memorySize = 256;

    environment.systemPackages = [ pkgs.curl pkgs.iproute2 ];

    services.stillframe-server = {
      enable = true;
      bindAddress = "127.0.0.1";
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

      def post(node, path, out):
          """POST the capability body and return the HTTP status as a string.

          Response headers are dumped alongside the body, at <out>.headers, so
          a caller can check them without issuing a second request.
          """
          return node.succeed(
              "curl -sS --max-time 30 -o {} -D {}.headers ".format(out, out)
              + "-w '%{http_code}' "
              + "-X POST -H 'Content-Type: application/json' "
              + "--data-binary @/tmp/request.json "
              + "http://127.0.0.1:{}{}".format(PORT, path)
          ).strip()


      def content_type(node, out):
          """The Content-Type of the response post() saved to <out>."""
          headers = node.succeed("cat {}.headers".format(out))
          for line in headers.splitlines():
              if line.lower().startswith("content-type:"):
                  return line.split(":", 1)[1].strip()
          return ""


      def exec_start(node):
          """The unit's single ExecStart= line, read back from the unit file."""
          unit = node.succeed("systemctl cat stillframe-server")
          lines = [l for l in unit.splitlines() if l.startswith("ExecStart=")]
          assert len(lines) == 1, "expected one ExecStart=, got {}".format(lines)
          return lines[0]


      def argv(node):
          """The running server's argv, from /proc/<pid>/cmdline.

          This, not the text of ExecStart=, is what the quoting has to get
          right: systemd splits that line on whitespace, and only the argv the
          process actually received proves a path containing a space survived
          in one piece. (lib.escapeShellArg adds quotes only where they are
          needed, so the ExecStart text itself is not a fixed string.)
          """
          pid = node.succeed(
              "systemctl show -p MainPID --value stillframe-server"
          ).strip()
          raw = node.succeed("tr '\\0' '\\n' < /proc/{}/cmdline".format(pid))
          # cmdline ends with a trailing NUL, so tr leaves a trailing newline.
          return raw.split("\n")[:-1]


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

          # Packed panel bytes are opaque. Without the header net/http sniffs
          # them, and a proxy that believes the sniff is free to rewrite the
          # image the firmware clocks straight out to the panel.
          with subtest("responses declare octet-stream ({})".format(name)):
              got = content_type(node, "/tmp/calibration.bin")
              assert got == "application/octet-stream", (
                  "calibrationImage Content-Type is {!r}".format(got)
              )
              status = post(node, "/clearImage", "/tmp/clear.bin")
              assert status == "200", "clearImage returned HTTP {}".format(status)
              got = content_type(node, "/tmp/clear.bin")
              assert got == "application/octet-stream", (
                  "clearImage Content-Type is {!r}".format(got)
              )

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

      # An operator-supplied imgDir is served straight from that path, with no
      # state directory conjured up behind their back — and every argument is
      # shell-escaped, so a directory whose name contains a space reaches the
      # process as one argument instead of two.
      with subtest("imgDir set leaves the unit untouched"):
          exec_start(machine)  # exactly one ExecStart=
          got = argv(machine)
          assert got[0].endswith("/bin/server"), "unexpected argv: {!r}".format(got)
          assert got[1:] == ["-bind", "0.0.0.0", "18450", "/srv/my photos"], (
              "unexpected argv: {!r}".format(got)
          )
          state = machine.succeed(
              "systemctl show -p StateDirectory --value stillframe-server"
          ).strip()
          assert state == "", "unexpected StateDirectory {!r}".format(state)

      # With imgDir unset the server is pointed at a directory systemd owns and
      # creates for it, so the daemon comes up with a readable (if empty) image
      # source rather than a path that does not exist. ProtectSystem=strict
      # makes the whole filesystem read-only, so this also pins that systemd's
      # special handling of StateDirectory still gets the path created.
      with subtest("imgDir unset gets a state directory"):
          exec_start(imgless)  # exactly one ExecStart=
          got = argv(imgless)
          assert got[1:] == [
              "-bind",
              "127.0.0.1",
              "18450",
              "/var/lib/stillframe-server/images",
          ], "unexpected argv: {!r}".format(got)
          state = imgless.succeed(
              "systemctl show -p StateDirectory --value stillframe-server"
          ).strip()
          assert state == "stillframe-server/images", (
              "unexpected StateDirectory {!r}".format(state)
          )
          imgless.succeed("test -d /var/lib/stillframe-server/images")

      # bindAddress narrows the listener rather than merely being accepted by
      # the module: on the imgless node the socket must exist on loopback and
      # nowhere else.
      with subtest("bindAddress binds only what it names"):

          def listeners(node):
              """Local Address:Port of every socket listening on PORT."""
              rows = [l.split() for l in node.succeed("ss -Hltn").splitlines()]
              return sorted(r[3] for r in rows if r[3].endswith(":{}".format(PORT)))

          got = listeners(imgless)
          assert got == ["127.0.0.1:{}".format(PORT)], (
              "bindAddress = 127.0.0.1 is not listening on loopback alone: "
              "{!r}".format(got)
          )

          # The default is still every interface. Note what that looks like:
          # Go opens a dual-stack AF_INET6 socket whenever the listen address
          # is a wildcard, so 0.0.0.0 shows up here as "*:<port>" — one socket
          # accepting IPv4 and IPv6 alike, exactly as the hardcoded 0.0.0.0 in
          # http.ListenAndServe did before this option existed.
          wildcard = [
              "*:{}".format(PORT),
              "0.0.0.0:{}".format(PORT),
              "[::]:{}".format(PORT),
          ]
          got = listeners(machine)
          assert got and all(addr in wildcard for addr in got), (
              "the default bindAddress is not listening on every interface: "
              "{!r}".format(got)
          )

      # The sandbox is not decoration: if one of these silently stopped being
      # applied, nothing else in this test would notice.
      with subtest("the unit is sandboxed"):
          for node in (machine, imgless):
              for prop, want in [
                  ("ProtectSystem", "strict"),
                  ("ProtectHome", "read-only"),
                  ("NoNewPrivileges", "yes"),
                  ("PrivateTmp", "yes"),
                  ("PrivateDevices", "yes"),
                  ("RestrictRealtime", "yes"),
                  ("RestrictSUIDSGID", "yes"),
                  ("LockPersonality", "yes"),
                  ("MemoryMax", "1073741824"),
              ]:
                  got = node.succeed(
                      "systemctl show -p {} --value stillframe-server".format(prop)
                  ).strip()
                  assert got == want, (
                      "{} is {!r}, want {!r}".format(prop, got, want)
                  )
              caps = node.succeed(
                  "systemctl show -p CapabilityBoundingSet --value stillframe-server"
              ).strip()
              assert caps == "", "CapabilityBoundingSet is {!r}".format(caps)
    '';
}
