# The habit CLI (cmd/habit), Habit Helper's interface to its data, from the
# agents Go module (the same vendored modules as default.nix, which leaves
# cmd/habit out). Static (CGO_ENABLED=0), so it runs in any sandbox
# image. Its tests gate the build: the integration test runs every command
# against PostgREST serving agents/habit-helper/schema on a throwaway
# Postgres. Built by agents/outputs.nix as habit-cli.
{ buildGoModule, postgresql, postgresqlTestHook, postgrest, agents, schema }:
buildGoModule {
  pname = "habit-cli";
  version = "0.1.0";
  src = ./.;
  inherit (agents) vendorHash;
  # internal/habitadmin, the admin chat, tests its store against the same
  # PostgREST.
  subPackages = [ "cmd/habit" "internal/habitadmin" ];
  ldflags = [ "-s" "-w" ];

  nativeCheckInputs = [ postgresql postgresqlTestHook postgrest ];
  # The log shows each test, and the integration test's commands and output.
  checkFlags = [ "-v" ];
  # The test creates the habit-helper roles and database as postgres, the
  # throwaway cluster's superuser.
  env = {
    CGO_ENABLED = "0";
    AGENTS_TEST_POSTGRES = "1";
    HABIT_TEST_SCHEMA = "${schema}";
  };

  meta.mainProgram = "habit";
}
