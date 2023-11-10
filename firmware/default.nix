{ pkgs, ... }:

pkgs.stdenv.mkDerivation rec {
  name = "platformio-project";

  nativeBuildInputs = [ pkgs.platformio ];

  src = ./.;

  buildPhase = ''
    platformio run
  '';

  installPhase = ''
    cp -r .pio/build/* $out/
  '';

  __noChroot = true;
}
