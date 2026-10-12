# Builds the desired-state JSON from the evaluated agents/declaration.nix:
# what agents-apply publishes to the gateway, and the copy the worker reads
# from the agents-desired-state ConfigMap.
#
#   gateway, workspace          as declared
#   profiles.<id>               file: the profile YAML; spec: its content as JSON
#   providers.<name>            profile: the profile id it is created from
#   types.<name>
#     image                     ref: <registry>/<image name>:<store hash>;
#                               archive: the tarball to push; cmd: its Cmd
#     policy                    file: the policy YAML; spec: its content as JSON
#     providers, env, resources as declared (resources null = gateway default)
#     session                   idleTimeoutSeconds, retentionSeconds, primeMessages,
#                               defaultWakeupSeconds (null: no wake-ups), timezone
#     version                   sha256 of everything above except the store
#                               paths (archive, file), plus the specs of the
#                               profiles behind the type's providers
#
# Also fails the build if a profile's id is not its attribute name, an image
# has no Cmd, which can only be read once the image config is built, or a
# timezone is not in tzdata.
{ pkgs, lib, config }:
let
  failed = lib.filter (a: !a.assertion) config.assertions;

  seconds = d:
    let
      m = builtins.match "([0-9]+)([smhd])" d;
      unit = { s = 1; m = 60; h = 3600; d = 86400; }.${builtins.elemAt m 1};
    in
    lib.toInt (builtins.head m) * unit;

  storeHash = drv:
    builtins.substring 0 32 (lib.removePrefix "${builtins.storeDir}/" drv.outPath);

  # The image's config JSON, with the Cmd under .config.
  imageConfig = image:
    if image ? stream.conf then image.stream.conf
    else pkgs.writeText "${baseNameOf image.imageName}-config.json"
      (builtins.toJSON { config = image.buildArgs.config or { }; });

  skeleton = {
    inherit (config) gateway workspace;
    profiles = lib.mapAttrs (_: file: { file = "${file}"; }) config.profiles;
    providers = lib.mapAttrs (_: p: { inherit (p) profile; }) config.providers;
    types = lib.mapAttrs (_: t: {
      image = {
        ref = "${config.registry}/${t.image.imageName}:${storeHash t.image}";
        archive = "${t.image}";
      };
      imageConfig = "${imageConfig t.image}"; # read and removed by the builder
      policy.file = "${t.policy}";
      inherit (t) providers env resources;
      session = {
        idleTimeoutSeconds = seconds t.session.idleTimeout;
        retentionSeconds = seconds t.session.retention;
        defaultWakeupSeconds =
          if t.session.defaultWakeup == null then null else seconds t.session.defaultWakeup;
        inherit (t.session) primeMessages timezone;
      };
    }) config.types;
  };
in
lib.throwIf (failed != [ ])
  "agents/declaration.nix:\n${lib.concatMapStringsSep "\n" (a: "- ${a.message}") failed}"
  (pkgs.runCommand "agents-desired-state.json"
    {
      nativeBuildInputs = [ pkgs.jq pkgs.yq-go ];
      skeleton = builtins.toJSON skeleton;
      passAsFile = [ "skeleton" ];
    } ''
    fail() { echo "agents desired state: $*" >&2; exit 1; }
    state=$(cat "$skeletonPath")

    for id in $(jq -r '.profiles | keys[]' <<< "$state"); do
      file=$(jq -r --arg id "$id" '.profiles[$id].file' <<< "$state")
      spec=$(yq -o json . "$file") || fail "profile $id: $file is not YAML"
      got=$(jq -r '.id' <<< "$spec")
      [ "$got" = "$id" ] || fail "profile $id: $file has id \"$got\""
      state=$(jq --arg id "$id" --argjson spec "$spec" \
        '.profiles[$id].spec = $spec' <<< "$state")
    done

    for t in $(jq -r '.types | keys[]' <<< "$state"); do
      file=$(jq -r --arg t "$t" '.types[$t].policy.file' <<< "$state")
      policy=$(yq -o json . "$file") || fail "type $t: policy $file is not YAML"
      [ "$(jq -r type <<< "$policy")" = object ] || fail "type $t: policy $file is not a mapping"

      conf=$(jq -r --arg t "$t" '.types[$t].imageConfig' <<< "$state")
      cmd=$(jq -c '.config.Cmd // []' "$conf")
      [ "$(jq length <<< "$cmd")" -gt 0 ] || fail "type $t: the image has no Cmd"

      tz=$(jq -r --arg t "$t" '.types[$t].session.timezone' <<< "$state")
      [ -f "${pkgs.tzdata}/share/zoneinfo/$tz" ] || fail "type $t: timezone $tz is not in tzdata"

      state=$(jq --arg t "$t" --argjson policy "$policy" --argjson cmd "$cmd" '
        .types[$t].policy.spec = $policy
        | .types[$t].image.cmd = $cmd
        | del(.types[$t].imageConfig)
      ' <<< "$state")

      version=$(jq -cS --arg t "$t" '
        . as $s | $s.types[$t] as $type
        | {
            type: ($type | del(.image.archive, .policy.file)),
            profiles: [$type.providers[] | $s.providers[.].profile | $s.profiles[.].spec]
          }
      ' <<< "$state" | sha256sum | cut -d ' ' -f 1)
      state=$(jq --arg t "$t" --arg v "$version" '.types[$t].version = $v' <<< "$state")
    done

    jq -S . <<< "$state" > $out
  '')
