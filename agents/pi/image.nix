# Sandbox image for the pi agent: pi from nixpkgs, a small shell toolset, and
# its config under /sandbox/.pi/agent. The sandbox policy is not in the image:
# it is passed at sandbox creation (agents/declaration.nix).
{ pkgs }:
let
  # The k8s driver runs sandboxes as uid/gid 10001, ignoring the image's User,
  # and seeds the sandbox's volume from the image's /sandbox, re-owned to it.
  uid = "10001";

  root = pkgs.buildEnv {
    name = "pi-agent-root";
    paths = with pkgs; [
      pi-coding-agent
      bashInteractive
      coreutils
      findutils
      diffutils
      gnugrep
      gnused
      gawk
      gnutar # openshell sandbox upload/download and --upload extract with tar
      gzip
      less
      git
      iproute2
      cacert
    ];
    pathsToLink = [ "/bin" "/etc" "/share" ];
  };
in
pkgs.dockerTools.buildLayeredImage {
  name = "pi-agent";
  tag = "latest";
  contents = [ root ];
  fakeRootCommands = ''
    mkdir -p usr tmp sandbox/.pi/agent
    ln -s /bin usr/bin
    chmod 1777 tmp

    echo 'root:x:0:0::/root:/bin/bash' > etc/passwd
    echo 'sandbox:x:${uid}:${uid}::/sandbox:/bin/bash' >> etc/passwd
    echo 'root:x:0:' > etc/group
    echo 'sandbox:x:${uid}:' >> etc/group

    cp ${./models.json} sandbox/.pi/agent/models.json
    cp ${./settings.json} sandbox/.pi/agent/settings.json
    cp ${./instructions.md} sandbox/.pi/agent/AGENTS.md
    chown -R ${uid}:${uid} sandbox
    chmod -R u+w sandbox
  '';
  enableFakechroot = true;
  config = {
    User = "${uid}:${uid}";
    WorkingDir = "/sandbox";
    # The sandbox's main process only has to keep running: the work arrives
    # through exec, one `pi -p` per turn.
    Cmd = [ "/bin/sleep" "infinity" ];
    Env = [
      "HOME=/sandbox"
      "PATH=/bin"
      "PI_CODING_AGENT_DIR=/sandbox/.pi/agent"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
    ];
  };
}
