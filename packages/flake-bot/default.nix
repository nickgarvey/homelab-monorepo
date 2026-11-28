# The flake-bot Go module (cmd/flake-bot): the UpdateRun and DevBuild
# Temporal workflows and their activities (ait nix-b77Zn). buildGoModule runs
# `go test ./...`, so the tests gate the build; one of them drives real git
# against a local repo. Built by ./outputs.nix.
{ buildGoModule, git }:
buildGoModule {
  pname = "flake-bot";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-VsBk088CAE2A6Y46HpORuAdQe3QTdqopUmyImGubrp8=";
  env.CGO_ENABLED = "0";
  nativeCheckInputs = [ git ];
  meta.mainProgram = "flake-bot";
}
