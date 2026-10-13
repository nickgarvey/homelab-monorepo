# Agent types published to the k8s OpenShell gateway by agents-apply; the
# options are in agents/schema.nix. Sandboxes of these types are made by the
# agents worker, one per conversation, in the workspace below.
{ images, profiles, ... }:
{
  gateway = "k8s";
  workspace = "agents";

  profiles.litellm = profiles.piLitellm;
  providers.litellm.profile = "litellm";

  # Habit Helper's data through PostgREST; only habit-helper sandboxes get
  # a token for it (k8s/manifests/habit-helper).
  profiles.habit-data = profiles.habitData;
  providers.habit-data.profile = "habit-data";

  # The Matrix bridge's admin API, Habit Helper's admin commands; only
  # habit-admin sandboxes get a token for it (k8s/manifests/agents).
  profiles.habit-admin-api = profiles.habitAdminApi;
  providers.habit-admin-api.profile = "habit-admin-api";

  types.pi = {
    image = images.pi;
    policy = ./pi/policy.yaml;
    providers = [ "litellm" ];
  };

  # Habit Helper (epic nix-psvkl): tracks the user's habits and supports them,
  # through the habit CLI. Wake-ups back off from daily while the user is
  # silent: 1d, 1d, 1d, 3d, then 7d.
  types.habit-helper = {
    image = images.habitHelper;
    policy = ./habit-helper/policy.yaml;
    providers = [ "litellm" "habit-data" ];
    session.defaultWakeup = "24h";
    session.timezone = "America/Los_Angeles";
  };

  # Habit Admin (nix-LAjpH.6): the user's plain-words console for Habit
  # Helper's habits and wake-ups, through the habit-admin CLI. No wake-ups:
  # it only answers.
  types.habit-admin = {
    image = images.habitAdmin;
    policy = ./habit-admin/policy.yaml;
    providers = [ "litellm" "habit-admin-api" ];
  };
}
