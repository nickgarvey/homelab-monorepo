# agents outputs: sandbox images and OpenShell provider profiles for the
# agents run on the OpenShell gateway, the desired state built from
# agents/declaration.nix, agents-apply, which publishes it, the images of
# agents-worker, which runs the chat sessions, and of agent-token, the token
# service for sandbox credentials, and of Habit Helper's PostgREST, and the
# habit and habit-admin CLIs.
# Imported by the root flake.nix.
{ self, nixpkgs, ... }:
let
  system = "x86_64-linux";
  pkgs = nixpkgs.legacyPackages.${system};
  lib = nixpkgs.lib;

  # The provider profile's binaries entry must be the exe that opens the
  # connection: the node that pi's wrapper execs, resolved past symlinks.
  # Fails the build if pi stops running under pkgs.nodejs.
  piLitellmProfile = pkgs.runCommand "pi-agent-litellm-profile.yaml" { } ''
    grep -qF '${pkgs.nodejs}/bin/node' ${pkgs.pi-coding-agent}/bin/.pi-wrapped
    node=$(readlink -f ${pkgs.nodejs}/bin/node)
    substitute ${./pi/litellm-profile.yaml} $out --replace-fail @node@ "$node"
  '';

  # The habit CLI, Habit Helper's only way to its data; its build runs the
  # integration test against PostgREST with this schema.
  habitCli = pkgs.callPackage ../packages/agents/habit-cli.nix {
    agents = agentsGo;
    schema = ./habit-helper/schema;
  };

  # The habit-data profile's binaries entry: the habit CLI, resolved past
  # symlinks (the sandbox matches the exe a connection comes from).
  habitDataProfile = pkgs.runCommand "habit-data-profile.yaml" { } ''
    habit=$(readlink -f ${lib.getExe habitCli})
    substitute ${./habit-helper/habit-data-profile.yaml} $out --replace-fail @habit@ "$habit"
  '';

  # The habit-admin CLI, the habit-admin agent's way to the bridge's admin
  # API.
  habitAdminCli = pkgs.callPackage ../packages/agents/habit-admin-cli.nix {
    agents = agentsGo;
  };

  # The habit-admin-api profile's binaries entry: the habit-admin CLI,
  # resolved past symlinks, like habit-data's.
  habitAdminApiProfile = pkgs.runCommand "habit-admin-api-profile.yaml" { } ''
    cli=$(readlink -f ${lib.getExe habitAdminCli})
    substitute ${./habit-admin/admin-api-profile.yaml} $out --replace-fail @habit-admin@ "$cli"
  '';

  # What a declaration can refer to.
  images.pi = import ./pi/image.nix { inherit pkgs; };
  images.habitHelper = import ./habit-helper/image.nix { inherit pkgs; habit = habitCli; };
  images.habitAdmin = import ./habit-admin/image.nix { inherit pkgs; habitAdmin = habitAdminCli; };
  profiles.piLitellm = piLitellmProfile;
  profiles.habitData = habitDataProfile;
  profiles.habitAdminApi = habitAdminApiProfile;

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

  # Pushed as oci.garvey.sh/agent-token:<tag>, tagged like agents-worker, and
  # run by k8s/manifests/agents/agent-token-deployment.yaml. It talks only to
  # the SPIRE agent's socket, so it needs no CA certificates.
  agentTokenImage = pkgs.dockerTools.buildLayeredImage {
    name = "agent-token";
    tag = builtins.substring 11 32 agentsGo.outPath;
    contents = pkgs.buildEnv {
      name = "image-root";
      paths = [ agentsGo ];
      pathsToLink = [ "/bin" ];
    };
    config = {
      Entrypoint = [ "/bin/agent-token" ];
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
  packages.${system} = {
    pi-agent-image = images.pi;
    habit-helper-image = images.habitHelper;
    pi-agent-litellm-profile = piLitellmProfile;
    agents-desired-state = desiredState ./declaration.nix;
    agents-apply = agentsApply;
    agents-worker-image = agentsWorkerImage;
    agent-token-image = agentTokenImage;
    habit-data-profile = habitDataProfile;
    habit-cli = habitCli;
    habit-admin-image = images.habitAdmin;
    habit-admin-api-profile = habitAdminApiProfile;
    habit-admin-cli = habitAdminCli;
    # Pushed as oci.garvey.sh/habit-helper-postgrest:<tag> and run by
    # k8s/manifests/habit-helper/deployment.yaml.
    habit-helper-postgrest-image = import ./habit-helper/postgrest-image.nix { inherit pkgs; };
  };

  checks.${system} = {
    agents-declaration-test = import ./declaration-test.nix { inherit pkgs lib images desiredState; };
    agents-go-tests = agentsGo;
    habit-cli-tests = habitCli;
    habit-admin-cli-tests = habitAdminCli;
  };
}
