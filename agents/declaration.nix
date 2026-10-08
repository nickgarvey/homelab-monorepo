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
}
