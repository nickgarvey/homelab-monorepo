# Prints a console line for each device the kernel shuts down at reboot, so a hang in device shutdown shows the stuck driver.
#
# joor and frul sometimes hang after systemd reaches "System Reboot", inside
# the kernel's device_shutdown(), and need a power cycle (nix-k2KT9.20,
# nix-k2KT9.24, nix-k2KT9.25). The iTCO watchdog cannot reset these boards:
# the BIOS routes the TCO timeout to SMI and locks the TCO config.
#
# With initcall_debug, device_shutdown() logs "<device>: shutdown" at
# KERN_INFO before each device's shutdown callback. boot.consoleLogLevel = 7
# puts KERN_INFO on the console (NixOS's own loglevel= param and kernel.printk
# sysctl both come from it); a loglevel= in kernelParams alone is overridden.
# On a hang, the last "shutdown" line on the console names the stuck device.
{ lib, inputs, ... }:
let
  expired = lib.max inputs.self.lastModified inputs.nixpkgs.lastModified >= 1796083200; # 2026-12-01T00:00:00Z
in
{
  boot.kernelParams = [ "initcall_debug" ];
  boot.consoleLogLevel = 7;

  assertions = [
    {
      assertion = !expired;
      message = ''
        modules/services/shutdown-debug.nix (nix-k2KT9.25) expired on
        2026-12-01. Remove it and its imports from joor, zah and frul, or push
        the deadline out deliberately.
      '';
    }
  ];
}
