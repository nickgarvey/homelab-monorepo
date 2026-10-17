# Touch UI on the BTT HDMI5 panel: Load, Unload and Stop buttons, with moonraker, klipper and kernel logs below (./screen.py).
{ config, pkgs, ... }:

let
  python = pkgs.python3.withPackages (ps: [ ps.pygame ps.evdev ]);
  fonts = "${pkgs.dejavu_fonts}/share/fonts/truetype";
in
{
  # The UI owns tty1's framebuffer; a login prompt there would only sit
  # underneath it. The serial console on ttyAMA0 stays.
  systemd.services."getty@tty1".enable = false;
  systemd.services."autovt@tty1".enable = false;

  systemd.services.skyforge-screen = {
    description = "Touch UI on the printer's screen";
    wantedBy = [ "multi-user.target" ];
    after = [ "moonraker.service" ];
    path = [ config.systemd.package ];
    environment = {
      SCREEN_FONT_SANS = "${fonts}/DejaVuSans.ttf";
      SCREEN_FONT_BOLD = "${fonts}/DejaVuSans-Bold.ttf";
      SCREEN_FONT_MONO = "${fonts}/DejaVuSansMono.ttf";
    };
    serviceConfig = {
      ExecStart = "${python}/bin/python3 ${./screen.py}";
      DynamicUser = true;
      # fb0, the touch panel, tty1 (to switch it to graphics mode) and the
      # journal for the log panes.
      SupplementaryGroups = [ "video" "input" "tty" "systemd-journal" ];
      # KDSETMODE on a tty that is not our controlling terminal.
      AmbientCapabilities = [ "CAP_SYS_TTY_CONFIG" ];
      CapabilityBoundingSet = [ "CAP_SYS_TTY_CONFIG" ];
      Restart = "always";
      RestartSec = 2;
    };
  };
}
