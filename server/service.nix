{ overlay }:

{ config, pkgs, lib, ... }:
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
        ExecStart = "${pkgs.stillframe.server}/bin/server ${toString cfg.port} ${imgDir}";
        # Only when we are the ones inventing the directory. An operator-supplied
        # imgDir is left entirely alone — the unit is then byte-identical to what
        # it was before imgDir became optional.
        StateDirectory = lib.mkIf (cfg.imgDir == null) stateImgDir;
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";
      };
    };
  };
}
