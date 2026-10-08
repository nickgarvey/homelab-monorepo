# agents outputs: sandbox images and OpenShell provider profiles for the
# agents run on the OpenShell gateway, the desired state built from
# agents/declaration.nix, agents-apply, which publishes it, and the image of
# agents-worker, which runs the chat sessions. Imported by the root flake.nix.
{ self, nixpkgs, ... }:
let
  system = "x86_64-linux";
  pkgs = nixpkgs.legacyPackages.${system};
  lib = nixpkgs.lib;

  # The gateway, supervisor and sandbox runtime are our own builds of OpenShell
  # with NVIDIA/OpenShell#3702 applied (packages/openshell, nix-lNoiG); without
  # it no sandbox can resolve a name in this IPv6-only cluster. Carrying the
  # patch is meant to be temporary.
  patched = builtins.pathExists ../patches/openshell-ipv6-policy-dns.patch;
  pinExpired = patched
    && lib.max self.lastModified nixpkgs.lastModified >= 1796083200; # 2026-12-01T00:00:00Z
  checkPin = lib.throwIf pinExpired ''
    patches/openshell-ipv6-policy-dns.patch (NVIDIA/OpenShell#3702, nix-lNoiG)
    expired on 2026-12-01. Move to a release that includes #3702, drop the
    patch and this check in agents/outputs.nix, or push the deadline out
    deliberately.
  '';

  # The provider profile's binaries entry must be the exe that opens the
  # connection: the node that pi's wrapper execs, resolved past symlinks.
  # Fails the build if pi stops running under pkgs.nodejs.
  piLitellmProfile = pkgs.runCommand "pi-agent-litellm-profile.yaml" { } ''
    grep -qF '${pkgs.nodejs}/bin/node' ${pkgs.pi-coding-agent}/bin/.pi-wrapped
    node=$(readlink -f ${pkgs.nodejs}/bin/node)
    substitute ${./pi/litellm-profile.yaml} $out --replace-fail @node@ "$node"
  '';

  # What a declaration can refer to.
  images.pi = import ./pi/image.nix { inherit pkgs; };
  profiles.piLitellm = piLitellmProfile;

  # evalAgents <module>: the declaration evaluated against agents/schema.nix.
  evalAgents = declaration: lib.evalModules {
    modules = [ ./schema.nix declaration ];
    specialArgs = { inherit images profiles; };
  };
  desiredState = declaration:
    import ./desired-state.nix { inherit pkgs lib; inherit (evalAgents declaration) config; };

  # packages/agents: agents-apply and agents-worker, tested by its build.
  agentsGo = pkgs.callPackage ../packages/agents { };

  # Pushed as oci.garvey.sh/agents-worker:<tag>, the tag being the package's
  # store hash, and run by k8s/manifests/agents/deployment.yaml.
  agentsWorkerImage = pkgs.dockerTools.buildLayeredImage {
    name = "agents-worker";
    tag = builtins.substring 11 32 agentsGo.outPath;
    contents = pkgs.buildEnv {
      name = "image-root";
      # cacert: RGW's certificate is a public one.
      paths = [ agentsGo pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };
    config = {
      Entrypoint = [ "/bin/agents-worker" ];
      # The Deployment's fsGroup and spiffe-helper's runAsUser match it.
      User = "1000:1000";
    };
  };

  # `nix run .#agents-apply [-- --dry-run]`: publishes agents/declaration.nix
  # with the CLIs it drives, the openshell one at the gateway's version.
  agentsApply = pkgs.writeShellApplication {
    name = "agents-apply";
    runtimeInputs = [ self.packages.${system}.openshell pkgs.skopeo pkgs.kubectl ];
    text = ''
      exec ${lib.getExe agentsGo} --state ${desiredState ./declaration.nix} "$@"
    '';
  };
in
{
  packages.${system} = lib.mapAttrs (_: checkPin) {
    pi-agent-image = images.pi;
    pi-agent-litellm-profile = piLitellmProfile;
    agents-desired-state = desiredState ./declaration.nix;
    agents-apply = agentsApply;
    agents-worker-image = agentsWorkerImage;
  };

  checks.${system} = lib.mapAttrs (_: checkPin) {
    agents-declaration-test = import ./declaration-test.nix { inherit pkgs lib images desiredState; };
    agents-go-tests = agentsGo;
  };
}
