final: prev: {
  dynamic-frame = {
    inherit (final.callPackage ./server { }) server smartcrop;
  };
}
