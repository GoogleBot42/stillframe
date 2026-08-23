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

    # Removes all phases except these two
    phases = [ "installPhase" "installCheckPhase" ];

    installPhase = ''
      mkdir -p $out

      # copy files
      cp -r $src/smartcrop-cli.py $out

      mkdir -p $out/bin
      makeWrapper ${python3}/bin/python3 $out/bin/smartcrop-cli \
        --prefix PYTHONPATH : ${makePythonPath [smartcrop]} \
        --add-flags "$out/smartcrop-cli.py"
    '';

    # The server treats a failing cropper as a soft error and falls back to a
    # centered crop, so a smartcrop-cli that cannot run at all is invisible at
    # runtime - which is exactly how smartcrop.py's use of the long-removed
    # Image.ANTIALIAS came to break every crop silently. Prove the thing runs
    # against the Pillow it is actually packaged with, and still prints the
    # integer JSON the Go side unmarshals.
    doInstallCheck = true;

    installCheckPhase = ''
      PYTHONPATH=${makePythonPath [ pillow ]} ${python3}/bin/python3 -c \
        "from PIL import Image; Image.new('RGB', (240, 180), (30, 60, 90)).save('probe.png')"

      $out/bin/smartcrop-cli probe.png 1200 1600 > crop.json

      ${python3}/bin/python3 -c "
      import json
      crop = json.load(open('crop.json'))
      for key in ('x', 'y', 'width', 'height'):
          assert isinstance(crop[key], int), (key, crop)
      assert crop['width'] > 0 and crop['height'] > 0, crop
      "
    '';
  };

  server = buildGoModule rec {
    pname = "stillframe-server";
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

      # --prefix (not --set "...:$PATH"): $PATH would be expanded at BUILD time,
      # baking the sandbox's stdenv (gcc, binutils, patchelf, make, ...) into the
      # wrapper and therefore into the runtime closure of a network-facing
      # daemon. smartcrop-cli is the only thing the server needs to find.
      wrapProgram $out/bin/server \
        --prefix PATH : "${smartcrop-cli}/bin"
    '';

    meta.mainProgram = "server";
  };
in
{
  server = serverWrapped;
  smartcrop = smartcrop-cli;
}
