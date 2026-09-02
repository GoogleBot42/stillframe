{ overlay }:

{ config, pkgs, lib, utils, ... }:
let
  cfg = config.services.stillframe-server;
  # The state directory systemd creates and hands the unit when imgDir is not
  # set. Named once here because the same string has to appear both as a
  # relative StateDirectory= and as an absolute path in ExecStart.
  stateImgDir = "stillframe-server/images";
  imgDir = if cfg.imgDir != null then cfg.imgDir else "/var/lib/${stateImgDir}";
in
{
  options.services.stillframe-server = {
    enable = lib.mkEnableOption "enable stillframe-server";
    imgDir = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "/var/lib/stillframe/images";
      description = ''
        Directory of images that the server will serve.

        Null, the default, makes the unit use the systemd-managed state
        directory /var/lib/stillframe-server/images instead, which is created
        empty and stays empty until someone puts photos in it. That is a
        working configuration rather than a broken one: the local directory is
        only ever an image source, so a /fetchImage with nothing available is
        answered 500 and the frame simply keeps the picture it is already
        showing. It is the right setting when Immich (see environmentFile) is
        the real source of photos, and it means an Immich-only deployment
        evaluates without inventing a directory for it.

        Set it to an existing photo directory to serve from there instead.
      '';
    };
    user = lib.mkOption {
      type = lib.types.str;
      default = "stillframe-server";
      description = ''
        The user the server should run as
      '';
    };
    group = lib.mkOption {
      type = lib.types.str;
      default = "stillframe-server";
      description = ''
        The group the server should run as
      '';
    };
    port = lib.mkOption {
      type = lib.types.int;
      default = 18450;
      description = ''
        The port the server runs on
      '';
    };
    bindAddress = lib.mkOption {
      type = lib.types.str;
      default = "0.0.0.0";
      example = "127.0.0.1";
      description = ''
        The address the server listens on. The default, 0.0.0.0, listens on
        every interface, which is what the frames on the LAN expect.

        Narrow it when something else is meant to be in front: 127.0.0.1 to
        reach the server only through a reverse proxy on the same host (the
        only way the endpoints are ever authenticated — see README.md), or a
        specific interface address, such as the host's Tailscale address, to
        serve frames on the tailnet and nobody else.

        Passed as a `-bind` flag ahead of the positional arguments, so the
        `server <port> <imgDir>` invocation below is unchanged when this is
        left at its default.
      '';
    };
    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/stillframe-server.env";
      description = ''
        Path to a systemd EnvironmentFile of KEY=value lines, which is how the
        optional Immich image source is configured: IMMICH_URL, IMMICH_API_KEY
        and IMMICH_ALBUM (see README.md). The server takes them from the
        environment rather than from its arguments, because the ExecStart below
        interpolates the port and the image directory by position.

        A file rather than an option per variable, because IMMICH_API_KEY is a
        full-access credential: anything set in the Nix expression ends up in
        the world-readable store, while a file read at start-up can be a secret
        rendered by sops-nix or agenix and owned by this service's user. Null,
        the default, leaves the unit exactly as it was before this option
        existed.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    nixpkgs.overlays = [ overlay ];
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
    };
    users.groups.${cfg.group} = { };
    systemd.services.stillframe-server = {
      enable = true;
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        # escapeSystemdExecArgs, not escapeShellArg: systemd's own quoting is
        # not the shell's. It splits ExecStart on whitespace — so an imgDir
        # containing a space used to arrive as two arguments and the server
        # served the first half of the path — but it also expands "%" specifiers
        # and "$VAR" references, inside quotes as well as out. A path like
        # /srv/2024%best would silently become the boot ID (or fail the unit
        # outright on an unknown specifier), and one containing "$" would lose
        # the rest of the word. escapeSystemdExecArgs handles all three: it
        # quotes, and it doubles "%" and "$" into their literal forms.
        ExecStart = "${pkgs.stillframe.server}/bin/server " + utils.escapeSystemdExecArgs [
          "-bind"
          cfg.bindAddress
          (toString cfg.port)
          imgDir
        ];
        # Only when we are the ones inventing the directory. An operator-supplied
        # imgDir is left entirely alone: no state directory is conjured up
        # behind their back, and the path is used exactly as given.
        StateDirectory = lib.mkIf (cfg.imgDir == null) stateImgDir;
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";

        # Sandboxing. The daemon is one static Go binary: it reads image files,
        # opens a listening socket, and (with Immich configured) makes outbound
        # HTTPS requests. It spawns nothing, writes nothing, and needs no
        # privilege at all — so almost everything can be taken away.
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        AmbientCapabilities = "";
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        # read-only rather than "true": an operator's imgDir may perfectly well
        # live under /home (or /root), and "true" would make it invisible.
        # Read-only keeps those paths readable while still refusing writes.
        ProtectHome = "read-only";
        ProtectProc = "invisible";
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectControlGroups = true;
        # AF_INET/AF_INET6 for the listener and for the outbound Immich
        # requests; AF_UNIX because that is how the process reaches journald
        # and a local resolver socket; AF_NETLINK because glibc's getaddrinfo
        # opens one to sort the addresses it returns, so leaving it out would
        # break name resolution — and therefore Immich — while the VM test,
        # which has no network at all, stayed green.
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        SystemCallArchitectures = "native";
        # Deliberately not also "~@resources": the Go runtime raises its own
        # RLIMIT_NOFILE with setrlimit at start-up, which lives in that set, and
        # a blocked call here is a SIGSYS rather than an error return.
        SystemCallFilter = [ "@system-service" "~@privileged" ];
        # main.go measured a conversion at 8 Mpx peaking around 520 MB and set
        # maxPixels to 4 Mpx to halve that, so a request at the cap costs on the
        # order of 260 MB and maxConcurrentRequests allows two at once. 1G is
        # roughly twice that worst case, and vastly more than the normal one —
        # panel-sized images, a few tens of MB. On a host with more RAM than
        # that it means a pathological request restarts this one service
        # instead of triggering the OOM killer somewhere else. On a 1 GB
        # Raspberry Pi the cap sits at physical RAM and never binds, so the
        # global OOM killer still goes first — hence mkDefault: an operator on
        # a small host can lower it with a plain assignment instead of mkForce.
        MemoryMax = lib.mkDefault "1G";
      };
    };
  };
}
