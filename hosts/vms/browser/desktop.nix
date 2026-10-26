# XFCE on X11 at a fixed 1280x800, auto-logged-in as claude, with Chromium (DevTools on 127.0.0.1:9222, persistent profile) kept running in the session.

{ lib, pkgs, ... }:

let
  # Anthropic's computer-use guidance: 1280x800 for web apps, and screenshots
  # at screen size need no coordinate scaling.
  width = 1280;
  height = 800;

  setResolution = pkgs.writeShellScript "set-resolution" ''
    PATH=${lib.makeBinPath [ pkgs.xrandr pkgs.libxcvt pkgs.gawk pkgs.gnugrep pkgs.gnused pkgs.coreutils ]}
    out=$(xrandr | awk '/ connected/ { print $1; exit }')
    if ! xrandr | grep -q '^ *${toString width}x${toString height} '; then
      xrandr --newmode ${toString width}x${toString height} \
        $(cvt ${toString width} ${toString height} | sed -n 's/^Modeline "[^"]*" *//p')
      xrandr --addmode "$out" ${toString width}x${toString height}
    fi
    xrandr --output "$out" --mode ${toString width}x${toString height}
  '';

  # Restarted when it exits, so Playwright always has a browser to attach to.
  # DevTools binds to 127.0.0.1 only; the MCP servers reach it from inside
  # the VM. Chromium requires a non-default --user-data-dir for remote
  # debugging.
  chromiumSession = pkgs.writeShellScript "chromium-session" ''
    while true; do
      ${pkgs.chromium}/bin/chromium \
        --remote-debugging-port=9222 \
        --user-data-dir="$HOME/.local/share/chromium-claude" \
        --password-store=basic \
        --no-first-run \
        --no-default-browser-check \
        --start-maximized
      sleep 2
    done
  '';

  autostart = name: exec: {
    "xdg/autostart/${name}.desktop".text = ''
      [Desktop Entry]
      Type=Application
      Name=${name}
      Exec=${exec}
      X-XFCE-Autostart-Override=true
    '';
  };
in
{
  # The session user. No wheel, no SSH keys: it is reached only through the
  # MCP servers (hosts/vms/browser/mcp.nix).
  users.users.claude = {
    isNormalUser = true;
    uid = 1001;
    description = "Desktop session that Claude drives";
  };

  services.xserver = {
    enable = true;
    desktopManager.xfce = {
      enable = true;
      enableScreensaver = false;
    };
    # Never blank or power down the screen: screenshots must show the desktop.
    serverFlagsSection = ''
      Option "BlankTime" "0"
      Option "StandbyTime" "0"
      Option "SuspendTime" "0"
      Option "OffTime" "0"
    '';
    # Draw the pointer into the framebuffer. With virtio-gpu, modesetting
    # puts it on a hardware cursor plane that QEMU sends as a separate SPICE
    # cursor channel, which the Incus web UI console does not draw.
    deviceSection = ''
      Option "SWcursor" "on"
    '';
  };
  services.displayManager.autoLogin = {
    enable = true;
    user = "claude";
  };

  # incus-virtual-machine.nix pulls in the minimal profile, which turns these
  # off; a desktop needs them.
  xdg.autostart.enable = true;
  xdg.icons.enable = true;
  xdg.mime.enable = true;

  # xfce4-power-manager turns DPMS back on with its own timers (standby at
  # 10 minutes, off at 15) whatever the server flags say. With the monitor
  # off, the SPICE console shows "no display active". These are the
  # system-wide defaults; the session user has no overrides.
  environment.etc = autostart "set-resolution" setResolution
    // autostart "chromium-session" chromiumSession
    // {
      "xdg/xfce4/xfconf/xfce-perchannel-xml/xfce4-power-manager.xml".text = ''
        <?xml version="1.0" encoding="UTF-8"?>
        <channel name="xfce4-power-manager" version="1.0">
          <property name="xfce4-power-manager" type="empty">
            <property name="dpms-enabled" type="bool" value="false"/>
            <property name="blank-on-ac" type="int" value="0"/>
            <property name="dpms-on-ac-sleep" type="uint" value="0"/>
            <property name="dpms-on-ac-off" type="uint" value="0"/>
          </property>
        </channel>
      '';
    };

  # Clipboard sharing with SPICE clients (incus console --type=vga), over the
  # com.redhat.spice.0 port Incus gives every VM.
  services.spice-vdagentd.enable = true;

  environment.systemPackages = [ pkgs.chromium ];
}
