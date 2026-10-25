# Time sync for every host: chrony, following the router's NTP server, with the public pool as fallback.
#
# dragonsreach serves NTP to the LAN from NTS-authenticated upstreams
# (modules/router/ntp.nix, which overrides the servers below on the router
# itself). Following one nearby source keeps hosts within about a millisecond
# of each other: the Ceph mons warn past 50 ms (MON_CLOCK_SKEW), and
# timesyncd's single pool server every ~34 min left them tens of ms apart.
#
# `prefer` makes chrony follow the router whenever it is reachable and sane.
# The pool lines are only a fallback for a router outage. They are
# unauthenticated, so chrony uses them only when the router is gone.
# The chrony module turns timesyncd off.
{ ... }:
{
  # The source lines go in extraConfig because the module's serverOption takes
  # only iburst or offline, not prefer.
  services.chrony = {
    enable = true;
    servers = [ ];
    extraConfig = ''
      server router.home.garvey.sh iburst prefer
      pool 2.nixos.pool.ntp.org iburst maxsources 2
    '';
  };
}
