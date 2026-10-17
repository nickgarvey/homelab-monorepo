# BOSL2, the OpenSCAD standard library (rounding, threading, attachments), laid
# out as an OpenSCAD library directory for OPENSCADPATH.
{ lib, stdenvNoCC, fetchFromGitHub }:

stdenvNoCC.mkDerivation {
  pname = "bosl2";
  version = "0-unstable-2026-10-17";

  src = fetchFromGitHub {
    owner = "BelfrySCAD";
    repo = "BOSL2";
    rev = "f1493a8425f51bd0585dd6d89aea962561100d32";
    hash = "sha256-h5nfbmCp0XKffZ5cvMTeasket1RE+VHanJfO7ALWwrk=";
  };

  dontBuild = true;

  installPhase = ''
    runHook preInstall
    mkdir -p $out/share/openscad/libraries/BOSL2
    cp *.scad LICENSE $out/share/openscad/libraries/BOSL2/
    runHook postInstall
  '';

  meta = {
    description = "The Belfry OpenScad Library, v2";
    homepage = "https://github.com/BelfrySCAD/BOSL2";
    license = lib.licenses.bsd2;
  };
}
