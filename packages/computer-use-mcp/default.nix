# MCP server that drives an X11 desktop with xdotool and scrot, with tools named after Anthropic's computer_toolset_20260801.
{ lib, python3, writeShellApplication, xdotool, scrot }:

let
  python = python3.withPackages (ps: [ ps.mcp ps.pillow ]);
in
writeShellApplication {
  name = "computer-use-mcp";
  runtimeInputs = [ xdotool scrot ];
  text = ''
    exec ${python}/bin/python3 -I ${./computer_use_mcp.py} "$@"
  '';
}
