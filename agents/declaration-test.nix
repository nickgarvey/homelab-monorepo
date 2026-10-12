# Checks that agents/schema.nix rejects bad declarations at evaluation time:
# each case below breaks one rule of a declaration that is otherwise valid, and
# must fail to evaluate. Run as the agents-declaration-test flake check.
{ pkgs, lib, images, desiredState }:
let
  profile = pkgs.writeText "test-profile.yaml" "id: test\n";

  valid = {
    gateway = "k8s";
    workspace = "agents";
    profiles.test = profile;
    providers.test.profile = "test";
    types.pi = {
      image = images.pi;
      policy = ./pi/policy.yaml;
      providers = [ "test" ];
    };
  };

  # A declaration: the valid one with `change` merged over it.
  with' = change: _: lib.recursiveUpdate valid change;

  bad = {
    undeclared-provider = with' { types.pi.providers = [ "test" "missing" ]; };
    undeclared-profile = with' { providers.other.profile = "missing"; };
    repeated-provider = with' { types.pi.providers = [ "test" "test" ]; };
    not-an-image = with' { types.pi.image = pkgs.hello; };
    stream-not-tarball = with' {
      types.pi.image = pkgs.dockerTools.streamLayeredImage { name = "x"; };
    };
    uppercase-type = with' { types.Pi = valid.types.pi; };
    double-hyphen-provider = with' {
      providers.a--b.profile = "test";
    };
    underscore-profile = with' { profiles.a_b = profile; };
    long-workspace = with' { workspace = "a-workspace-name-too-long"; };
    bad-gateway = with' { gateway = "../k8s"; };
    bad-env-name = with' { types.pi.env."1X" = "y"; };
    bad-duration = with' { types.pi.session.idleTimeout = "6 hours"; };
    zero-prime = with' { types.pi.session.primeMessages = 0; };
    bad-wakeup = with' { types.pi.session.defaultWakeup = "1 day"; };
    bad-timezone = with' { types.pi.session.timezone = "../etc/passwd"; };
    bad-memory = with' { types.pi.resources.memory = "lots"; };
    unknown-option = with' { types.pi.command = [ "pi" ]; };
  };

  evaluates = declaration:
    (builtins.tryEval (desiredState declaration).drvPath).success;

  wronglyAccepted = lib.attrNames (lib.filterAttrs (_: evaluates) bad);
in
assert lib.assertMsg (evaluates (with' { })) "the valid test declaration fails to evaluate";
pkgs.runCommand "agents-declaration-test" { } (
  if wronglyAccepted == [ ] then "touch $out"
  else ''
    echo "agents/schema.nix accepts these bad declarations (agents/declaration-test.nix):"
    echo "  ${lib.concatStringsSep " " wronglyAccepted}"
    exit 1
  ''
)
