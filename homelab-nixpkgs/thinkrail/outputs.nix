# ThinkRail — worktree IDE host for the pi coding agent. Imported by homelab-nixpkgs/outputs.nix.
{ nixpkgs }:
let
  # The CLI artifact is produced by `bun build --compile`, which appends the
  # bundle to a copy of the *running* bun — so a build only ever targets its
  # own host. Each system also needs its own vendored-dependency hash (the
  # lockfile resolves platform-specific optional deps), so adding a system
  # means adding its hash below, not just widening this list.
  supportedSystems = [ "x86_64-linux" ];
  forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
  pkgsFor = system: nixpkgs.legacyPackages.${system};

  version = "0-unstable-2026-09-04";

  srcFor = system: (pkgsFor system).fetchFromGitHub {
    owner = "JetBrains";
    repo = "thinkrail";
    rev = "73ee056747168fc0fc699a842d90f16fa38caba5";
    hash = "sha256-bc5jKWgPjnO2EdWci/0kpnKXJkQAqHVzqrM39gDr9sM=";
  };

  # `bun install` for the whole workspace, as a fixed-output derivation so
  # the real build can stay offline. Bun's isolated layout is a tree of
  # relative symlinks under node_modules/.bun plus one node_modules per
  # workspace member, so the install is captured as those directories at
  # their original paths and laid back over the source tree at build time.
  nodeModulesHashes = {
    x86_64-linux = "sha256-+FcaslYQYJx71AE0RbQ+bNp7O/cmvU3L8UFLiPPwUMI=";
  };

  nodeModulesFor = system:
    let pkgs = pkgsFor system; in
    pkgs.stdenvNoCC.mkDerivation {
      pname = "thinkrail-node-modules";
      inherit version;
      src = srcFor system;

      nativeBuildInputs = [ pkgs.bun ];

      dontConfigure = true;
      dontFixup = true;

      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR
        export BUN_INSTALL_CACHE_DIR=$TMPDIR/bun-cache
        bun install --frozen-lockfile --ignore-scripts --no-progress
        runHook postBuild
      '';

      installPhase = ''
        runHook preInstall
        find . -type d -name node_modules -prune -print0 |
          while IFS= read -r -d "" dir; do
            mkdir -p "$out/$(dirname "$dir")"
            cp -a "$dir" "$out/$dir"
          done
        runHook postInstall
      '';

      outputHashAlgo = "sha256";
      outputHashMode = "recursive";
      outputHash = nodeModulesHashes.${system};
    };

  thinkrailFor = system:
    let
      pkgs = pkgsFor system;
      # Everything the host resolves from PATH at runtime. The in-process pi
      # engine wants Node 22.19+; the rest are shelled out to by name, so a
      # missing one surfaces as a broken feature rather than a link error:
      #   git       every worktree operation (git/gitExec.ts)
      #   zenity    the "open a project" folder picker, which tries zenity
      #             then kdialog and otherwise reports that neither exists
      #             (dialog/dialog.ts)
      #   procps    pgrep, to notice a terminal tab still has a running
      #             process before closing it (terminal/shellBusy.ts)
      #   xdg-utils xdg-open, to reach a browser on start-up without
      #             --no-open (apps/cli/src/bootstrap.ts)
      # gh, the user's editors and JetBrains' central stay out on purpose:
      # each is probed with Bun.which and degrades on its own.
      runtimeDeps = [
        pkgs.git
        pkgs.nodejs_22
        pkgs.zenity
        pkgs.procps
        pkgs.xdg-utils
      ];
    in
    pkgs.stdenv.mkDerivation {
      pname = "thinkrail";
      inherit version;
      src = srcFor system;

      nativeBuildInputs = [ pkgs.bun pkgs.nodejs_22 pkgs.git pkgs.makeWrapper ];

      configurePhase = ''
        runHook preConfigure
        cp -a ${nodeModulesFor system}/. .
        chmod -R u+w .
        # The vendored CLIs are `#!/usr/bin/env node` scripts, and neither
        # /usr/bin/env nor an unpatched `node` exists in the sandbox.
        find . -type d -name node_modules -prune -print0 |
          while IFS= read -r -d "" dir; do
            patchShebangs --build "$dir"
          done
        runHook postConfigure
      '';

      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR
        # turbo phones home for telemetry and update checks on first run.
        export TURBO_TELEMETRY_DISABLED=1
        export TURBO_NO_UPDATE_NOTIFIER=1
        export DO_NOT_TRACK=1
        bun run build:binary
        runHook postBuild
      '';

      installPhase = ''
        runHook preInstall
        install -Dm755 apps/cli/dist/thinkrail $out/libexec/thinkrail/thinkrail
        makeWrapper $out/libexec/thinkrail/thinkrail $out/bin/thinkrail \
          --suffix PATH : ${nixpkgs.lib.makeBinPath runtimeDeps}
        runHook postInstall
      '';

      # The compiled artifact is a bun runtime with the bundle appended to
      # it; strip discards the payload and leaves a binary that exits with
      # "Failed to open executable as zip".
      dontStrip = true;

      meta = {
        description = "Desktop/browser IDE host that runs the pi coding agent over git worktrees";
        homepage = "https://thinkrail.ai/";
        license = nixpkgs.lib.licenses.asl20;
        mainProgram = "thinkrail";
        platforms = supportedSystems;
      };
    };
in
{
  packages = forAllSystems (system: rec {
    thinkrail = thinkrailFor system;
    default = thinkrail;
    node-modules = nodeModulesFor system;
  });

  devShells = forAllSystems (system:
    let pkgs = pkgsFor system; in
    {
      default = pkgs.mkShell {
        packages = [ pkgs.bun pkgs.nodejs_22 pkgs.git ];
      };
    });
}
