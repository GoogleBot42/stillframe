{ pkgs, ... }:

pkgs.stdenv.mkDerivation rec {
  name = "platformio-project";

  nativeBuildInputs = [ pkgs.platformio ];

  src = ./.;

  buildPhase = ''
    platformio run
  '';

  installPhase = ''
    mkdir -p $out/bin
    cp .pio/build/*/firmware.bin $out/bin/
  '';

  __noChroot = true;
}
