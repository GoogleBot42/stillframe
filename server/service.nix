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
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";
      };
    };
  };
}
