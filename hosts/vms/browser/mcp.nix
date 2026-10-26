# MCP servers that Claude reaches with `incus exec` (docs/browser-vm.md): Playwright attached to the session's Chromium, and computer-use for the rest of the desktop.

{ pkgs, ... }:

let
  computerUseMcp = pkgs.callPackage ../../../packages/computer-use-mcp { };

  # incus exec runs as root with no session environment. Both servers run as
  # the session user, against its X display and its Chromium.
  asSessionUser = name: command: pkgs.writeShellScriptBin name ''
    exec ${pkgs.util-linux}/bin/runuser -u claude -- ${pkgs.coreutils}/bin/env -i \
      HOME=/home/claude USER=claude PATH=/run/current-system/sw/bin \
      DISPLAY=:0 XAUTHORITY=/home/claude/.Xauthority \
      ${command}
  '';
in
{
  environment.systemPackages = [
    (asSessionUser "browser-mcp-playwright"
      # nixpkgs' wrapper sets PLAYWRIGHT_MCP_ISOLATED unless this is set, and
      # isolated over CDP means a fresh context in a second window, without
      # the profile's logins. Set, Playwright drives the default context.
      "PLAYWRIGHT_MCP_USER_DATA_DIR=/home/claude/.local/share/chromium-claude ${pkgs.playwright-mcp}/bin/playwright-mcp --cdp-endpoint http://127.0.0.1:9222")
    (asSessionUser "browser-mcp-computer" "${computerUseMcp}/bin/computer-use-mcp")
  ];
}
