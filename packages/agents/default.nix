# The agents Go module: agents-apply (cmd/agents-apply), which publishes the
# agent types declared in agents/declaration.nix, and agents-worker
# (cmd/agents-worker), which runs the chat sessions on Temporal.
# buildGoModule runs `go test`, so the tests gate the build; the store and
# session tests run against a throwaway Postgres started by
# postgresqlTestHook. Built by agents/outputs.nix.
{ buildGoModule, postgresql, postgresqlTestHook }:
buildGoModule {
  pname = "agents";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-iLSotVecBBNtJ/uJ5iyFd0OFVjjuoJ2hz9GgtxQFR5U=";

  nativeCheckInputs = [ postgresql postgresqlTestHook ];
  # storetest gives each test package its own database.
  postgresqlTestUserOptions = "LOGIN CREATEDB";
  env.AGENTS_TEST_POSTGRES = "1";

  meta.mainProgram = "agents-apply";
}
