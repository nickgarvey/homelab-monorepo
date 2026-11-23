# The release-watch Go module (cmd/release-watch): the ReleaseWatch Temporal
# workflow, its activity and its metrics. buildGoModule runs `go test ./...`,
# so the tests, which fake GitHub, Kubernetes and Temporal, gate the build.
# Built by ./outputs.nix.
{ buildGoModule }:
buildGoModule {
  pname = "release-watch";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-8ZuDx8WuEOh9AZx/6uDT4jvpKzRLTVy88dE2mQkXvSY=";
  env.CGO_ENABLED = "0";
  meta.mainProgram = "release-watch";
}
