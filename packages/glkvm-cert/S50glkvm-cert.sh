# Starts glkvm-cert on the KVM at boot. /etc/init.d/S99custom sources every
# /etc/kvmd/user/scripts/S??*.sh with $1 set to start or stop; the directory is
# part of what the firmware carries across an update.
dir=/etc/kvmd/user/glkvm-cert
pidfile=/run/glkvm-cert.pid

case "$1" in
start)
    # Sourced, so this file needs no mode bits of its own; the binary's are
    # set here in case an update's restore does not keep them.
    chmod 0755 "$dir/glkvm-cert"
    start-stop-daemon -S -b -m -p "$pidfile" -x "$dir/glkvm-cert"
    ;;
stop)
    start-stop-daemon -K -q -p "$pidfile" -x "$dir/glkvm-cert"
    rm -f "$pidfile"
    ;;
esac
