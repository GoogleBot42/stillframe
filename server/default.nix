{ lib
, python3
, fetchFromGitHub
, makeWrapper
, buildGoModule
, stdenv
}:

with python3.pkgs;

let
  smartcrop = buildPythonPackage rec {
    pname = "smartcrop.py";
    version = "v0.3.2";

    src = fetchFromGitHub {
      owner = "smartcrop";
      repo = pname;
      rev = version;
      hash = "sha256-37ADx72OvORAan51CzdnDFS4uWH8DN/CaXSt5qjnLA4=";
    };

    propagatedBuildInputs = [
      pillow
      numpy
    ];

    format = "setuptools";

    doCheck = true;

    pythonImportsCheck = [
      "smartcrop"
    ];
  };

  smartcrop-cli = buildPythonApplication {
    pname = "smartcrop-cli";
    version = "v1";

    format = "other";

    src = ./.;

    nativeBuildInputs = [ makeWrapper ];

    phases = [ "installPhase" ]; # Removes all phases except installPhase

    installPhase = ''
      mkdir -p $out

      # copy files
      cp -r $src/smartcrop-cli.py $out

      mkdir -p $out/bin
      makeWrapper ${python3}/bin/python3 $out/bin/smartcrop-cli \
        --prefix PYTHONPATH : ${makePythonPath [smartcrop]} \
        --add-flags "$out/smartcrop-cli.py"
    '';
  };

  server = buildGoModule rec {
    pname = "dynamic-frame-server";
    version = "0.0.1";

    src = ./.;

    vendorHash = "sha256-qr3hNJxCT8YPQntKCCPNO2yaETswziGXAd4lQELsDGg=";
  };

  # Wrap server so it has access to smartcrop in its PATH
  serverWrapped = stdenv.mkDerivation {
    pname = server.pname;
    version = server.version;
    src = server;

    buildInputs = [ makeWrapper ];

    installPhase = ''
      mkdir -p $out/bin
      cp ${server}/bin/* $out/bin/

      wrapProgram $out/bin/server \
        --set PATH "${smartcrop-cli}/bin:$PATH"
    '';

    meta.mainProgram = "server";
  };
in
{
  server = serverWrapped;
  smartcrop = smartcrop-cli;
}
