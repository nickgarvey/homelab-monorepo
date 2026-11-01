# ThinkRail, JetBrains' agentic IDE around pi: the prebuilt `thinkrail` CLI,
# which serves the workbench UI to a browser.
#
# The release binary is Bun-compiled: the app is a payload appended to the ELF,
# which patchelf can corrupt, so it runs unmodified inside an FHS env that
# provides /lib64/ld-linux-x86-64.so.2. `thinkrail update` (and Settings →
# Updates) cannot replace a store path; bump `version` and `hash` here instead,
# taking the hash from the release's SHA256SUMS.
{ lib, fetchurl, stdenvNoCC, buildFHSEnv }:

let
  version = "0.3.0";

  unwrapped = stdenvNoCC.mkDerivation {
    pname = "thinkrail-unwrapped";
    inherit version;

    src = fetchurl {
      url = "https://github.com/JetBrains/thinkrail/releases/download/v${version}/thinkrail-linux-x64";
      sha256 = "888e64c6cd7ede8bce16ca0ef7bbf409ccedc9f1155dab8fe34386ab151eb516";
    };

    dontUnpack = true;
    dontStrip = true;
    dontPatchELF = true;

    installPhase = ''
      install -Dm755 $src $out/libexec/thinkrail
    '';
  };
in
buildFHSEnv {
  pname = "thinkrail";
  inherit version;

  # The agent and the workbench shell out to these: git for workspaces
  # (worktrees), gh for the pull-request flow, ssh for git remotes.
  targetPkgs = pkgs: with pkgs; [
    bashInteractive
    coreutils
    gh
    git
    openssh
  ];

  runScript = "${unwrapped}/libexec/thinkrail";

  meta = {
    description = "Agentic IDE around the pi coding agent";
    homepage = "https://github.com/JetBrains/thinkrail";
    license = lib.licenses.asl20;
    platforms = [ "x86_64-linux" ];
    mainProgram = "thinkrail";
  };
}
