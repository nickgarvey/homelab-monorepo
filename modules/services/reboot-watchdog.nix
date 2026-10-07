# Keeps the hardware watchdog running through the kernel's reboot path, so a host that hangs after systemd hands over to the kernel resets itself.
#
# joor and frul sometimes hang in the kernel's device shutdown after systemd
# reaches "System Reboot", and sit there until power-cycled (nix-k2KT9.20,
# nix-k2KT9.24). systemd arms the watchdog for RebootWatchdogSec (10min by
# default) before rebooting, but the kernel's watchdog reboot notifier stops
# it again before device shutdown runs. watchdog.stop_on_reboot=0 leaves it
# running, so a hung reboot ends in a hardware reset.
#
# The runtime watchdog makes systemd own /dev/watchdog from boot. Without it, a
# timer that firmware leaves running across the reset would have nothing
# pinging it and would reset the host partway through a normal boot.
{ ... }:
{
  boot.kernelParams = [ "watchdog.stop_on_reboot=0" ];

  systemd.settings.Manager.RuntimeWatchdogSec = "60s";
}
