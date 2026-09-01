{ overlay }:

{ config, pkgs, lib, ... }:
let
  cfg = config.services.stillframe-server;
in
{
  options.services.stillframe-server = {
    enable = lib.mkEnableOption "enable stillframe-server";
    imgDir = lib.mkOption {
      type = lib.types.str;
      description = ''
        Directory of images that the server will serve
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
        ExecStart = "${pkgs.stillframe.server}/bin/server ${toString cfg.port} ${cfg.imgDir}";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";
      };
    };
  };
}
