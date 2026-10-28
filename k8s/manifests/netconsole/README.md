# netconsole

Receives kernel netconsole UDP from hosts that import
`modules/services/netconsole.nix` (talos), so the last kernel messages before a
hang or panic are kept off the host. Vector prints each message on stdout and
appends it to `/var/log/netconsole/<sender>/<UTC date>.log` on the PVC. The
sender is the hostname the module tags each message with.

Senders are admitted by source address in `network-policy.yaml`; a new host
needs its static IPv6 there as well as the module import.

## Apply

```sh
kubectl apply -f manifests/netconsole/namespace.yaml
kubectl apply -f manifests/netconsole/
```

## Read

```sh
kubectl -n netconsole logs deploy/netconsole -f
```

The image is distroless (no shell, no tar), so read the files from an
ephemeral container that shares the pod's process namespace (it runs as the
pod's user, so it can read them):

```sh
kubectl -n netconsole debug -it "$(kubectl -n netconsole get pod -l app=netconsole -o name)" --image=docker.io/library/busybox:1.37 --target=vector -- \
  sh -c 'ls -R /proc/1/root/var/log/netconsole'
```

## Test

On the sender: `echo "netconsole test" | sudo tee /dev/kmsg`.
