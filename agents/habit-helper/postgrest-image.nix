# Image for Habit Helper's data API (k8s/manifests/habit-helper): PostgREST,
# and tern with the schema's migrations in /share/habit-helper/schema for the
# init container that applies them. The tag is the store hash of its
# contents, so a new migration or a nixpkgs bump gives a new tag.
{ pkgs }:
let
  schema = pkgs.runCommand "habit-helper-schema" { } ''
    mkdir -p $out/share/habit-helper
    cp -r ${./schema} $out/share/habit-helper/schema
  '';

  root = pkgs.buildEnv {
    name = "habit-helper-postgrest-root";
    paths = [ pkgs.postgrest pkgs.tern schema ];
    pathsToLink = [ "/bin" "/share" ];
  };
in
pkgs.dockerTools.buildLayeredImage {
  name = "habit-helper-postgrest";
  tag = builtins.substring 11 32 root.outPath;
  contents = [ root ];
  config = {
    Entrypoint = [ "/bin/postgrest" ];
    # The Deployment's fsGroup matches it, so the client keys are readable.
    User = "1000:1000";
    # PostgREST (GHC) reads its configuration as UTF-8 only with a UTF-8 locale.
    Env = [ "LC_ALL=C.UTF-8" ];
  };
}
