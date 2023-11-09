{ overlay }:

{ config, pkgs, lib, ... }:
let
  cfg = config.services.picture-frame-server;
in
{
  options.services.picture-frame-server = {
    enable = lib.mkEnableOption "enable picture-frame-server";
    user = lib.mkOption {
      type = lib.types.str;
      default = "picture-frame-server";
      description = ''
        The user the server should run as
      '';
    };
    group = lib.mkOption {
      type = lib.types.str;
      default = "picture-frame-server";
      description = ''
        The group the server should run as
      '';
    };
    imgDir = lib.mkOption {
      type = lib.types.str;
      description = ''
        Directory of images that the server will serve
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
    systemd.services.picture-frame-server = {
      enable = true;
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig.ExecStart = "${pkgs.picture-frame.server}/bin/server ${toString cfg.port} ${cfg.imgDir}";
      serviceConfig.User = cfg.user;
      serviceConfig.Group = cfg.group;
    };
  };
}
