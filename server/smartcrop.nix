{ lib
, python3
, fetchFromGitHub
, makeWrapper
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

in

# python3.withPackages (ps: with ps; [
#   smartcrop
# ])

smartcrop-cli