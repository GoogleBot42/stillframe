{ buildGoModule
}:

{
  # No wrapper: the cropper is in-process (pigo, with its cascade embedded via
  # //go:embed), so the server binary has no runtime PATH dependency at all.
  server = buildGoModule {
    pname = "stillframe-server";
    version = "0.0.1";

    src = ./.;

    vendorHash = "sha256-Vec4eTlyQP4knh0JmAijKIyr6ZT3JLeMMT+DSvGibbE=";

    meta.mainProgram = "server";
  };
}
