# Living-room TV appliance profile: boots straight into the Steam gamescope
# session, with Jellyfin and YouTube as controller-navigable apps alongside it.
{ config, lib, pkgs, ... }:

let
  # YouTube is a Chromium kiosk window; Jellyfin is the native client. Both are
  # started from Steam as non-Steam shortcuts, which hands them an environment
  # they cannot use as-is:
  #
  #   * LD_LIBRARY_PATH points into Steam's bundled steam-runtime, whose glib
  #     predates g_array_copy. Anything nix-built that pulls in libwacom dies
  #     at load time ("libwacom.so.9: undefined symbol: g_array_copy"). The
  #     nix binaries carry their own RPATHs, so drop it entirely.
  #   * LD_PRELOAD is Steam's gameoverlayrenderer.so. It breaks QtWebEngine's
  #     chromium zygote handshake outright ("Check failed: ReceiveFixedMessage
  #     ... zygote_host_impl_linux.cc"), killing Jellyfin Media Player before it
  #     draws. The overlay is no loss on a media app.
  #   * WAYLAND_DISPLAY is set but empty: gamescope blanks it so libwayland's
  #     wayland-0 fallback cannot reach a parent compositor. wlroots treats any
  #     set value as "use the wayland backend", so cage dies at startup
  #     ("Could not connect to remote display") instead of opening on X11.
  #
  # DISPLAY is deliberately left alone. The session runs gamescope with
  # --xwayland-count 2, so Steam puts each launched app on its own Xwayland
  # server; that second server is how gamescope knows which window to present.
  # Forcing a wayland connection to gamescope's own socket instead makes the
  # app render into a surface gamescope never shows.
  fromSteamShortcut = ''
    unset LD_LIBRARY_PATH LD_PRELOAD WAYLAND_DISPLAY
  '';
  mkChromiumApp = { name, desktopName, url, extraArgs ? [ ] }:
    let
      bin = pkgs.writeShellScriptBin name ''
        # --ozone-platform-hint picks X11, i.e. the Xwayland server Steam
        # launched this shortcut on.
        ${fromSteamShortcut}
        # Each app gets its own profile directory. Sharing chromium's default
        # profile would make the second app open a window inside the first
        # app's process and exit immediately, which Steam reads as the
        # shortcut having closed.
        exec ${pkgs.chromium}/bin/chromium \
          --user-data-dir="''${XDG_DATA_HOME:-$HOME/.local/share}/${name}" \
          --ozone-platform-hint=auto \
          --start-fullscreen \
          ${lib.concatStringsSep " \\\n  " extraArgs} \
          --app=${url}
      '';
    in
    [
      bin
      (pkgs.makeDesktopItem {
        inherit name desktopName;
        exec = "${bin}/bin/${name}";
        categories = [ "AudioVideo" "Video" ];
      })
    ];

  # youtube.com/tv is served only to large-screen clients, so the user-agent has
  # to claim to be one; in return the UI is D-pad navigable, which is what makes
  # it usable from the Steam Controller (the desktop site needs a mouse cursor).
  youtubeApp = mkChromiumApp {
    name = "youtube-tv";
    desktopName = "YouTube";
    url = "https://www.youtube.com/tv";
    extraArgs = [
      ''--user-agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.5 TV Safari/537.36"''
    ];
  };

  # Jellyfin Media Player, nested in a cage compositor. JMP is Qt+QtWebEngine
  # with mpv embedded as a render target; run directly under gamescope its
  # window never gets a GL context ("vo/libmpv: No render context set") and
  # never appears. cage gives it an ordinary wlroots compositor to draw into.
  # --tv selects the 10-foot interface, which the Steam Controller can drive.
  #
  # LAN Jellyfin is IPv6-only: jellyfin.garvey.sh resolves to the cluster's
  # Cilium LoadBalancer via the split-horizon override in networking/dns.nix.
  jellyfin-tv = pkgs.writeShellScriptBin "jellyfin-tv" ''
    ${fromSteamShortcut}
    # Qt keys its compiled-QML cache on the qrc: path, which never changes, so
    # a cached unit outlives package updates and silently keeps running the old
    # webview.qml - including the unpatched one. Not worth the debugging time.
    export QML_DISABLE_DISK_CACHE=1
    exec ${pkgs.cage}/bin/cage -- \
      ${pkgs.jellyfin-media-player}/bin/jellyfin-desktop --tv --fullscreen
  '';

  jellyfinApp = [
    jellyfin-tv
    (pkgs.makeDesktopItem {
      name = "jellyfin-tv";
      desktopName = "Jellyfin";
      exec = "${jellyfin-tv}/bin/jellyfin-tv";
      categories = [ "AudioVideo" "Video" ];
    })
  ];
in
{
  imports = [
    ../core/nixos-common.nix
    ./steam.nix
  ];

  nixpkgs.config.allowUnfree = true;

  # Boot straight into Big Picture. steam-gamescope comes from
  # programs.steam.gamescopeSession; tuigreet stays the default_session so
  # quitting Steam lands on a greeter instead of a dead TTY.
  services.greetd = {
    enable = true;
    settings = {
      initial_session = {
        command = "steam-gamescope";
        user = "ngarvey";
      };
      default_session = {
        command = "${pkgs.tuigreet}/bin/tuigreet --time --remember --cmd steam-gamescope";
        user = "greeter";
      };
    };
  };

  # This is a laptop acting as a set-top box: the lid is shut and the internal
  # panel is unused, so no lid or idle event may ever suspend it mid-movie.
  services.logind.settings.Login = {
    HandleLidSwitch = "ignore";
    HandleLidSwitchDocked = "ignore";
    HandleLidSwitchExternalPower = "ignore";
    HandlePowerKey = "poweroff";
    IdleAction = "ignore";
  };
  systemd.sleep.settings.Sleep = {
    AllowSuspend = "no";
    AllowHibernation = "no";
  };

  # Audio, over HDMI to the TV. The machine is a laptop, so it also exposes
  # built-in analog speakers that wireplumber would otherwise rank first;
  # demote them so sound always lands on the TV nobody is sitting next to.
  services.pipewire = {
    enable = true;
    pulse.enable = true;
    wireplumber.extraConfig."51-tv-default-sink" = {
      "monitor.alsa.rules" = [
        {
          matches = [ { "node.name" = "~alsa_output.*hdmi.*"; } ];
          actions.update-props."priority.session" = 2000;
        }
        {
          matches = [ { "node.name" = "~alsa_output.*analog.*"; } ];
          actions.update-props."priority.session" = 100;
        }
      ];
    };
  };

  # Recent silicon (Strix Point) wants a recent kernel, as on the workstations.
  boot.kernelPackages = pkgs.linuxPackages_latest;

  fonts.packages = with pkgs; [
    noto-fonts
    noto-fonts-color-emoji
  ];

  environment.variables.NIXOS_OZONE_WL = "1";

  time.timeZone = "America/Los_Angeles";

  # render + input are what gamescope and the Steam Controller need.
  users.users.ngarvey.extraGroups = [ "wheel" "networkmanager" "video" "render" "input" ];

  # Media apps. The Big Picture tiles for these live in a binary shortcuts.vdf
  # under the Steam account's userdata, which is per-account home state Nix does
  # not manage; what this module guarantees is that the binaries and desktop
  # entries exist for those tiles to point at.
  environment.systemPackages = jellyfinApp ++ youtubeApp ++ (with pkgs; [
    chromium
    mpv
  ]);

  # resolved is the NetworkManager DNS backend, so the LAN's split-horizon
  # jellyfin.garvey.sh override resolves to the cluster LoadBalancer.
  services.resolved = {
    enable = true;
    settings.Resolve.DNSSEC = "false";
  };
  networking.networkmanager.dns = "systemd-resolved";

  networking.firewall.logRefusedConnections = false;
}
