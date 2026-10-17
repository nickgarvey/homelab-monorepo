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
      # fb0, the touch panel, and the journal for the log panes.
      SupplementaryGroups = [ "video" "input" "systemd-journal" ];
      # systemd hands tty1 to the service user as its controlling terminal on
      # stdin, which is what lets screen.py switch it to graphics mode. Nothing
      # else opens tty1, so its mode at boot is 0600 root.
      TTYPath = "/dev/tty1";
      StandardInput = "tty";
      StandardOutput = "journal";
      StandardError = "journal";
      Restart = "always";
      RestartSec = 2;
    };
  };
}
