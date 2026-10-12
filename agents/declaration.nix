# Agent types published to the k8s OpenShell gateway by agents-apply; the
# options are in agents/schema.nix. Sandboxes of these types are made by the
# agents worker, one per conversation, in the workspace below.
{ images, profiles, ... }:
{
  gateway = "k8s";
  workspace = "agents";

  profiles.litellm = profiles.piLitellm;
  providers.litellm.profile = "litellm";

  types.pi = {
    image = images.pi;
    policy = ./pi/policy.yaml;
    providers = [ "litellm" ];
  };

  # pi with wake-ups, to try the worker's wake-ups out until an agent type of
  # its own uses them. To be removed by nix-psvkl.7.
  types.pi-wakeup = {
    image = images.pi;
    policy = ./pi/policy.yaml;
    providers = [ "litellm" ];
    session.defaultWakeup = "1h";
    session.timezone = "America/Los_Angeles";
  };
}
