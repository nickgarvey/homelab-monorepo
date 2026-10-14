# The agents Go module: agents-apply (cmd/agents-apply), which publishes the
# agent types declared in agents/declaration.nix, agents-worker
# (cmd/agents-worker), which runs the chat sessions on Temporal,
# agents-matrix (cmd/agents-matrix), the Matrix bridge, and agent-token
# (cmd/agent-token), which trades JWT-SVIDs for credentials.
# buildGoModule runs `go test`, so the tests gate the build; the store and
# session tests run against a throwaway Postgres started by
# postgresqlTestHook. Built by agents/outputs.nix.
#
# The habit CLI (cmd/habit) is built and tested by habit-cli.nix instead. It
# and the Nix files are left out of the source here, so changing them does
# not change these binaries' store paths or the image tags made from them.
{ lib, buildGoModule, postgresql, postgresqlTestHook }:
buildGoModule {
  pname = "agents";
  version = "0.1.0";
  src = builtins.path {
    path = ./.;
    name = "agents";
    filter = path: _type:
      !(builtins.elem (lib.removePrefix (toString ./. + "/") path) [ "cmd/habit" "default.nix" "habit-cli.nix" ]);
  };
  vendorHash = "sha256-+eZCgrKKASUFtt62xN47o0nPTCmjPHYYVtTYJgJDMWk=";

  nativeCheckInputs = [ postgresql postgresqlTestHook ];
  # storetest gives each test package its own database.
  postgresqlTestUserOptions = "LOGIN CREATEDB";
  env.AGENTS_TEST_POSTGRES = "1";

  meta.mainProgram = "agents-apply";
}
