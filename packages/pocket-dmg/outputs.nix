# pocket-dmg: drives an AYANEO Pocket DMG over adb (controller presses, screenshots, logs, apps).
# Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
in
{
  # bin/pocket-dmg runs on the host; share/pocket-dmg/dmgpad and dmgflash (controller
  # firmware flasher, see controller-fw/) are arm64
  # builds for the device. buildGoModule runs `go test`.
  package = pkgs.buildGoModule {
    pname = "pocket-dmg";
    version = "0.1.0";
    src = ./.;
    vendorHash = null; # no external deps
    subPackages = [ "cmd/pocket-dmg" ];
    nativeBuildInputs = [ pkgs.makeWrapper ];
    postBuild = ''
      GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dmgpad ./cmd/dmgpad
      GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dmgflash ./cmd/dmgflash
    '';
    postInstall = ''
      install -Dm755 dmgpad $out/share/pocket-dmg/dmgpad
      install -Dm755 dmgflash $out/share/pocket-dmg/dmgflash
      wrapProgram $out/bin/pocket-dmg --prefix PATH : ${pkgs.android-tools}/bin
    '';
  };
}
