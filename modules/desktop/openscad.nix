# OpenSCAD with BOSL2, plus a Claude Code skill for writing, rendering and checking printable parts.

{ pkgs, ... }:

let
  bosl2 = pkgs.callPackage ../../packages/bosl2 { };
in
{
  # openscad-unstable, not openscad: the latter is the 2021.01 release, without
  # the Manifold backend that renders in seconds what CGAL takes minutes over.
  environment.systemPackages = [ pkgs.openscad-unstable ];

  environment.variables.OPENSCADPATH = "${bosl2}/share/openscad/libraries";

  home-manager.users.ngarvey.home.file.".claude/skills/openscad/SKILL.md".source = ./openscad-skill.md;
}
