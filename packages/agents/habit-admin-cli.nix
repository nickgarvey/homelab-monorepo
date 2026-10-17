# The habit-admin CLI (cmd/habit-admin), the habit-admin agent's way to the
# Matrix bridge's admin API, from the agents Go module (the same vendored
# modules as default.nix, which leaves cmd/habit-admin out). Static
# (CGO_ENABLED=0), so it runs in any sandbox image. Built by
# agents/outputs.nix as habit-admin-cli.
{ buildGoModule, agents }:
buildGoModule {
  pname = "habit-admin-cli";
  version = "0.1.0";
  src = ./.;
  inherit (agents) vendorHash;
  subPackages = [ "cmd/habit-admin" ];
  ldflags = [ "-s" "-w" ];
  env.CGO_ENABLED = "0";

  meta.mainProgram = "habit-admin";
}
