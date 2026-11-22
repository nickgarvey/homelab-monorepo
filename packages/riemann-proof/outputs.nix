# riemann-proof: checks Levent Alpöge's Lean proof of the Riemann Hypothesis with two independent kernels.
# Imported by packages/outputs.nix.
#
# `nix run .#riemann-proof` builds riemann-proof-check, the derivation that runs the checkers, and
# prints its RESULT. The check derivation only builds if every check passes, so a successful build
# is the verdict. It runs on the local machine and is never substituted from a cache.
#
# What it checks is Andrew Sutherland's lean4export of Alpöge's module Main (solution.ndjson) and
# the five statements, proved by sorry and importing only Mathlib (challenge.ndjson); see
# https://math.mit.edu/~drew/grh/. What it trusts: that challenge.ndjson encodes Mathlib's
# statements (ait nix-EUkhx.6 would rebuild it from Mathlib), and the checkers themselves.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  inherit (pkgs) lib;

  # Checker worker count. con-leche held about 50 GB at 64 workers at Sutherland's site;
  # talos has 62 GiB.
  jobs = 16;

  grh = "https://math.mit.edu/~drew/grh";
  solution = pkgs.fetchurl {
    url = "${grh}/solution.ndjson.zst";
    sha256 = "61a8938ad5f5e57397c1c0e3b954916af546d9004ade01c3555744a681b05381";
  };
  # The published hash is of the unpacked file; this one was taken from a download whose unpacked
  # hash matched it.
  challenge = pkgs.fetchurl {
    url = "${grh}/challenge.ndjson.zst";
    sha256 = "c24ef99dab3556a52c7fb886e03cf22a242f3b96f82ae4cc40539e5d2f678ac6";
  };

  theorems = [
    "riemannHypothesis"
    "riemannZeta_eq_zero_re"
    "LFunction_ne_zero_of_half_lt_re"
    "LFunction_eq_zero_re"
    "LFunction_eq_zero_re_of_isPrimitive"
  ];

  # con-leche pins leanprover/lean4:v4.36.0; nixpkgs has 4.35.0, so this is the official release.
  lean = pkgs.stdenv.mkDerivation {
    pname = "lean4-bin";
    version = "4.36.0";
    src = pkgs.fetchurl {
      url = "https://github.com/leanprover/lean4/releases/download/v4.36.0/lean-4.36.0-linux.tar.zst";
      hash = "sha256-K3qx7YuS0uar+vQTRII7UQBk28nAJgysc+tJarLepus=";
    };
    nativeBuildInputs = [ pkgs.zstd pkgs.autoPatchelfHook ];
    buildInputs = [ pkgs.stdenv.cc.cc.lib pkgs.zlib ];
    dontConfigure = true;
    dontBuild = true;
    installPhase = ''
      mkdir -p $out
      cp -r ./* $out/
    '';
  };

  # con-leche: master checks every declaration; the compare branch checks the solution against
  # the challenge. Its lakefile has no dependencies, so lake builds it offline.
  conLeche = branch: rev: hash: pkgs.stdenv.mkDerivation {
    pname = "con-leche-${branch}";
    version = builtins.substring 0 8 rev;
    src = pkgs.fetchFromGitHub {
      owner = "leanprover";
      repo = "con-leche";
      inherit rev hash;
    };
    nativeBuildInputs = [ lean pkgs.git pkgs.autoPatchelfHook ];
    buildPhase = ''
      runHook preBuild
      export HOME=$TMPDIR
      [ "$(cat lean-toolchain)" = leanprover/lean4:v4.36.0 ]
      lake build con-leche
      runHook postBuild
    '';
    installPhase = ''
      install -Dm755 .lake/build/bin/con-leche $out/bin/con-leche
    '';
  };
  conLecheMaster = conLeche "master" "cd2eceab2e9b10404c663b468639c8aa99d0d1f2" "sha256-UyimwRmQCLG/SPnFdezKL8hdQ1SVIy0m4u9LCIp0rHY=";
  conLecheCompare = conLeche "compare" "5dc36dc15b7bee891a02406b78a8ac816a47baad" "sha256-4Jhn/1PhdPrmwcuoUsYCXZG8kI3yF6O8bcbEsFUA63A=";

  # con-ron: con-leche ported to Rust, a second implementation.
  conRon = pkgs.rustPlatform.buildRustPackage {
    pname = "con-ron";
    version = "3f22699b";
    src = pkgs.fetchFromGitHub {
      owner = "leanprover";
      repo = "con-ron";
      rev = "3f22699b8fd370f97fa9a784f7df7c5decf8e100";
      hash = "sha256-ScSKv+YlDUM1WbG96paTs+2OV8SPdJlHOe29I4Bo3nE=";
    };
    cargoHash = "sha256-cxUCIOZDmZ1F2YoNQ2+C5XRUxPowGHZdVJ7ZPhnOjUc=";
    cargoBuildFlags = [ "-p" "con-ron" ];
    doCheck = false;
  };

  check = pkgs.runCommand "riemann-proof-check"
    {
      nativeBuildInputs = [ pkgs.zstd pkgs.time conLecheMaster conLecheCompare conRon ];
      # The point is to check here, so never take the result from a cache.
      preferLocalBuild = true;
      allowSubstitutes = false;
      # Built on their own: nix build .#riemann-proof-check.conRon
      passthru = { inherit lean conLecheMaster conLecheCompare conRon; };
    }
    ''
      set -euo pipefail
      mkdir -p $out
      cd $TMPDIR
      note() { echo "$*" | tee -a summary; }

      # timed <label> <log> <cmd...>: runs cmd into log, records wall time and peak RSS.
      timed() {
        local label=$1 log=$2
        shift 2
        command time -f '%e %M' -o time.out "$@" > "$log" 2>&1 || { cat "$log"; echo "$label failed"; exit 1; }
        cp "$log" $out/
        local secs kb
        read -r secs kb < <(tail -n1 time.out)
        note "$label: ''${secs}s wall, $((kb / 1024)) MiB peak RSS"
      }
      expect() {
        grep -qF -- "$2" "$1" || { cat "$1"; echo "$1 lacks: $2"; exit 1; }
        note "  ok: $2"
      }
      unpack() {
        zstd -d -q --long="$2" -o "$3" "$1"
        [ "$(sha256sum "$3" | cut -d' ' -f1)" = "$4" ] || { echo "$3: wrong sha256"; exit 1; }
        note "$3: sha256 $4"
      }

      unpack ${solution} 30 solution.ndjson 5ea74b912147c5c93cc2b6d70cc727cf908df2f62ac6a20480a3fa02f6be244e
      unpack ${challenge} 27 challenge.ndjson bd484ac9886a96f26b1d9a7a59ebd45b7a70d102c015d4f31f9302bbdd6ab2b9

      timed "con-leche (every declaration)" con-leche.log \
        ${conLecheMaster}/bin/con-leche --jobs=${toString jobs} --progress=5% solution.ndjson
      expect con-leche.log "accepted 1468418 declarations"

      timed "con-leche --compare" con-leche-compare.log \
        ${conLecheCompare}/bin/con-leche --jobs=${toString jobs} --progress=10% --compare challenge.ndjson solution.ndjson
      timed "con-ron --compare" con-ron-compare.log \
        ${conRon}/bin/con-ron --jobs=${toString jobs} --progress=10% --compare challenge.ndjson solution.ndjson
      for log in con-leche-compare.log con-ron-compare.log; do
        note "''${log%.log} (it re-checks the whole solution too):"
        expect "$log" "(1468418 declarations accepted) (--verified)"
        ${lib.concatMapStrings (t: ''
          expect "$log" "theorem ${t} justified by the solution"
        '') theorems}
      done

      { echo "PASS: con-leche and con-ron accept the proof of all five statements"
        cat summary
        echo "Trusted: that challenge.ndjson encodes Mathlib's statements, and the two checkers."
      } > $out/RESULT
      cat $out/RESULT
    '';
in
{
  inherit check;

  # `nix run .#riemann-proof`: builds the check (here, if it is not already in the local store)
  # and prints its RESULT.
  package = pkgs.writeShellApplication {
    name = "riemann-proof";
    text = ''
      cat ${check}/RESULT
    '';
  };
}
