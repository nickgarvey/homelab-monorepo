# Options for agents/declaration.nix: the agent types published to an
# OpenShell gateway, their images, policies, provider profiles and providers,
# and the session settings the worker applies to their sandboxes. Evaluated
# with lib.evalModules by agents/outputs.nix; agents/desired-state.nix turns
# the result into the desired-state JSON.
{ config, lib, ... }:
let
  inherit (lib) mkOption types;

  # OpenShell's rules for profile ids (lowercase kebab-case) and, with a length
  # limit, for workspace and sandbox names (a DNS-1123 label with no "--", which
  # the Kubernetes driver uses as a separator). We hold provider and type names
  # to it too: provider names end up in paths in the sandbox, and type names in
  # workflow IDs and object keys.
  kebab = "[a-z0-9]+(-[a-z0-9]+)*";
  isKebab = name: builtins.match kebab name != null;

  # Workspace and sandbox names are at most 19 characters, so that
  # <workspace>--<sandbox> fits a Kubernetes name with room to spare.
  maxRoutableName = 19;

  duration = types.strMatching "[1-9][0-9]*[smhd]";

  # A tarball from dockerTools.buildImage or buildLayeredImage, which agents-apply
  # pushes with `skopeo copy docker-archive:`. Their image config (and so the Cmd)
  # can be read: from buildArgs, or from the layered image's conf.
  dockerImage = types.addCheck types.package (p:
    p ? imageName
    && (p ? buildArgs || p ? stream.conf)
    && builtins.match ".*\\.tar(\\.[a-z0-9]+)?" p.name != null
  ) // {
    description = "image tarball from dockerTools.buildImage or buildLayeredImage";
  };

  provider = types.submodule {
    options.profile = mkOption {
      type = types.str;
      description = "Profile the provider is created from (`provider create --type`).";
    };
  };

  agentType = types.submodule {
    options = {
      image = mkOption {
        type = dockerImage;
        description = ''
          The sandbox image. Its Cmd is the sandbox's main process, which the
          worker passes explicitly; for a chat agent it only has to keep running.
        '';
      };
      policy = mkOption {
        type = types.path;
        description = "Sandbox policy YAML, passed at sandbox creation (`--policy`).";
      };
      providers = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Providers attached to the type's sandboxes.";
      };
      env = mkOption {
        type = types.attrsOf types.str;
        default = { };
        description = "Non-secret environment for the sandbox (`--env`).";
      };
      resources = {
        cpu = mkOption {
          type = types.nullOr (types.strMatching "[0-9]+(\\.[0-9]+)?m?");
          default = null;
          description = "CPU limit (`--cpu`), e.g. 500m or 2; null for the gateway's default.";
        };
        memory = mkOption {
          type = types.nullOr (types.strMatching "[0-9]+(\\.[0-9]+)?([KMGTPE]i?)?");
          default = null;
          description = "Memory limit (`--memory`), e.g. 4Gi; null for the gateway's default.";
        };
      };
      session = {
        idleTimeout = mkOption {
          type = duration;
          default = "6h";
          description = "Idle time after which a session's sandbox is stopped.";
        };
        retention = mkOption {
          type = duration;
          default = "7d";
          description = "Time a stopped sandbox is kept before it is deleted, with its PVC.";
        };
        primeMessages = mkOption {
          type = types.ints.positive;
          default = 50;
          description = "Chat messages a freshly created sandbox is primed with.";
        };
        defaultWakeup = mkOption {
          type = types.nullOr duration;
          default = null;
          description = ''
            Enables wake-ups for the type: the worker runs a turn of its own
            in each conversation when the agent asks for one, or by default
            this long after the last wake-up. While the user stays silent the
            default backs off: 1, 1, 1, 3, then 7 times this, and 7 times
            from then on. Null: no wake-ups.
          '';
        };
        timezone = mkOption {
          type = types.strMatching "[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*";
          default = "UTC";
          description = "IANA time zone the prompt header gives the time in, e.g. America/Los_Angeles.";
        };
      };
    };
  };
in
{
  options = {
    gateway = mkOption {
      type = types.strMatching "[A-Za-z0-9][A-Za-z0-9._-]*";
      description = "Name of the gateway in the operator's CLI config (`-g`).";
    };
    workspace = mkOption {
      type = types.str;
      description = "OpenShell workspace holding the profiles, providers and the worker's sandboxes.";
    };
    registry = mkOption {
      type = types.str;
      default = "oci.garvey.sh";
      description = "Registry images are pushed to, as <registry>/<image name>:<store hash>.";
    };
    profiles = mkOption {
      type = types.attrsOf types.path;
      default = { };
      description = ''
        Provider profiles by id: each a file (or derivation producing one) of
        profile YAML whose `id` is the attribute name.
      '';
    };
    providers = mkOption {
      type = types.attrsOf provider;
      default = { };
      description = "Providers by name. None carries credentials.";
    };
    types = mkOption {
      type = types.attrsOf agentType;
      default = { };
      description = "Agent types by name.";
    };

    assertions = mkOption {
      type = types.listOf (types.submodule {
        options.assertion = mkOption { type = types.bool; };
        options.message = mkOption { type = types.str; };
      });
      internal = true;
      default = [ ];
      description = "Checks agents/desired-state.nix enforces before building.";
    };
  };

  config.assertions =
    let
      named = kind: names: map (name: {
        assertion = isKebab name;
        message = "${kind} name \"${name}\" must be lowercase kebab-case (a-z, 0-9, single hyphens)";
      }) names;
    in
    [
      {
        assertion = isKebab config.workspace
          && builtins.stringLength config.workspace <= maxRoutableName;
        message = "workspace \"${config.workspace}\" must be lowercase kebab-case and at most ${toString maxRoutableName} characters";
      }
    ]
    ++ named "profile" (builtins.attrNames config.profiles)
    ++ named "provider" (builtins.attrNames config.providers)
    ++ named "type" (builtins.attrNames config.types)
    ++ lib.mapAttrsToList (name: p: {
      assertion = config.profiles ? ${p.profile};
      message = "provider ${name}: profile \"${p.profile}\" is not declared";
    }) config.providers
    ++ lib.concatLists (lib.mapAttrsToList (name: t:
      map (p: {
        assertion = config.providers ? ${p};
        message = "type ${name}: provider \"${p}\" is not declared";
      }) t.providers
      ++ [{
        assertion = lib.allUnique t.providers;
        message = "type ${name}: providers are listed more than once";
      }]
      ++ map (key: {
        assertion = builtins.match "[A-Za-z_][A-Za-z0-9_]*" key != null;
        message = "type ${name}: env name \"${key}\" is not a valid variable name";
      }) (builtins.attrNames t.env)
    ) config.types);
}
