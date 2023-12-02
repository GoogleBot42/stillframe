{ overlay }:

{ config, pkgs, lib, ... }:
let
  cfg = config.services.picture-frame-server;
in
{
  options.services.picture-frame-server = {
    enable = lib.mkEnableOption "enable picture-frame-server";
    imgDir = lib.mkOption {
      type = lib.types.str;
      description = ''
        Directory of images that the server will serve
      '';
    };
    imgDirGroup = lib.mkOption {
      type = lib.types.str;
      description = ''
        The group the server will run as a member of.
        So the server can have read access to `imgDir`.
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
    systemd.services.picture-frame-server = {
      enable = true;
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        ExecStart = "${pkgs.picture-frame.server}/bin/server ${toString cfg.port} ${cfg.imgDir}";
        DynamicUser = true;
        PrivateTmp = true;
        User = "picture-frame-server";
        SupplementaryGroups = [ cfg.imgDirGroup ];
      };
    };
  };
}
