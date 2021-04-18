with import <nixpkgs> {};

buildGoModule {
  pname = "drone-convert-nix";
  version = "0.0.1";
  src = ./.;
  vendorSha256 = "sha256-VYwsVdU+uBYWRD42C8a2WX3Lmb29Gt1FDHAnxpgEMR0=";
  nativeBuildInputs = [ delve ];
  shellHook = ''
    unset GOFLAGS
  '';
  # delve does not compile with hardening enabled
  hardeningDisable = [ "all" ];
}
