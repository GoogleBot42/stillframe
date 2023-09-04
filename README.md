# DynamicFrame

To build go server `nix build .#server`

To build eink firmware `nix build --relaxed-sandbox .#firmware`

Eink firmware cannot build in the nix sandbox because platformio require internet access to build