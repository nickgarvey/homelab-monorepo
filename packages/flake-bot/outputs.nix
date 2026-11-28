# flake-bot, which wabbajack runs as a NixOS service
# (modules/services/flake-bot.nix). Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
in
{
  package = pkgs.callPackage ./. { };
}
