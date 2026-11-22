# full.sh: builds Levent Alpöge's Lean proof of the Riemann Hypothesis from source and checks it.
#
# Runs on wabbajack as a systemd unit, with every file under ~/riemann (HOME is pointed at
# ~/riemann/home, so elan, Mathlib's cache and git's config stay inside too). Start it with:
#
#   scp packages/riemann-proof/full.sh wabbajack:riemann/full.sh
#   ssh wabbajack 'sudo systemd-run --unit=riemann-full --collect --uid=$USER \
#     -p WorkingDirectory=$HOME/riemann -p Environment=HOME=$HOME/riemann/home \
#     -p LimitNOFILE=524288 -p MemoryMax=110G -p Nice=10 \
#     /run/current-system/sw/bin/nix shell nixpkgs#{bash,coreutils,gnugrep,gnused,findutils,gnutar,gzip,curl,git,time,elan,ripgrep} \
#     -c bash $HOME/riemann/full.sh'
#   ssh wabbajack 'sudo journalctl -fu riemann-full -o cat'        # follow it
#   ssh wabbajack 'cat ~/riemann/RESULT'                           # the verdict
#
# Every step resumes: a rerun skips the download if the archive's hash matches, and Lake rebuilds
# only modules not yet compiled. RESULT starts with PASS only if every step ran to the end.
#
# Steps: download and hash the archive, scan the source, check the pins, fetch Mathlib's cache,
# build Statements and Main, check the five theorems' axioms, replay Main with leanchecker.

set -euo pipefail
R=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$R"
mkdir -p "$HOME"
: >summary.txt
rm -f RESULT
done=no

finish() {
  local rc=$?
  [ -n "${janitor:-}" ] && kill "$janitor" 2>/dev/null || true
  local status="FAIL (exit $rc)"
  [ "$done" = yes ] && status=PASS
  { echo "$status: full build and check, $(date -u +%FT%TZ)"; cat summary.txt; } >RESULT
  echo "=== RESULT"
  cat RESULT
}
trap finish EXIT

log() { echo "=== $(date -u +%T) $*"; }
note() { echo "$*" | tee -a summary.txt; }
die() { note "FAILED: $*"; exit 1; }

# timed <label> <log> <cmd...>: runs cmd into log, records wall time and peak RSS.
timed() {
  local label=$1 out=$2
  shift 2
  log "$label (log: $R/$out)"
  local rc=0
  command time -f '%e %M' -o time.out "$@" >"$out" 2>&1 || rc=$?
  local secs kb
  read -r secs kb < <(tail -n1 time.out)
  note "$label: exit $rc, $(awk -v s="$secs" 'BEGIN { printf "%.1f", s / 3600 }') h wall, $((kb / 1024)) MiB peak RSS (largest single process)"
  [ "$rc" -eq 0 ] || { tail -n 40 "$out"; die "$label exited $rc"; }
}

theorems=(
  riemannHypothesis
  riemannZeta_eq_zero_re
  LFunction_ne_zero_of_half_lt_re
  LFunction_eq_zero_re
  LFunction_eq_zero_re_of_isPrimitive
)

# --- 1. The archive, from the author's site. The server answers 200 for any path, so only the
# joined hash counts.
want=9631b4f020199e580ba86178cb56c6e9b09f690c2369a80dc4334aa93f49c7fc
if [ ! -f lean.tar.gz ] || [ "$(sha256sum lean.tar.gz | cut -d' ' -f1)" != "$want" ]; then
  log "downloading https://alpo.ge/lean.tar.gz.part01..14"
  mkdir -p parts
  for i in $(seq -w 1 14); do
    curl -fsSL --retry 5 -o "parts/lean.tar.gz.part$i" "https://alpo.ge/lean.tar.gz.part$i"
  done
  cat parts/lean.tar.gz.part{01..14} >lean.tar.gz
  got=$(sha256sum lean.tar.gz | cut -d' ' -f1)
  [ "$got" = "$want" ] || die "lean.tar.gz sha256 is $got, expected $want"
  rm -rf parts
fi
note "lean.tar.gz: sha256 matches ($want), $(stat -c %s lean.tar.gz) bytes"

if [ ! -f lean/.extracted ]; then
  log "extracting"
  rm -rf lean
  tar -xzf lean.tar.gz
  touch lean/.extracted
fi
cd lean
note "source: $(find src -name '*.lean' | wc -l) .lean files"

# --- 2. Static scan of the project's own source (not Mathlib).
log "scanning src/"
{
  echo "## fatal patterns"
  rg -n --no-heading -e '\bsorry\b' -e '\badmit\b' -e 'native_decide' -e 'implemented_by' \
    -e '^\s*((private|protected|noncomputable|unsafe|partial)\s+)*axiom\b' \
    -e '^\s*(theorem|def|abbrev|structure|class)\s+RiemannHypothesis\b' src || true
  echo "## listed for review"
  rg -n --no-heading -e 'addDecl' -e 'run_cmd' -e 'run_elab' -e 'run_meta' -e 'IO\.Process' \
    -e '\bunsafe\b' -e '@\[extern' -e 'maxHeartbeats 0' src || true
} >../scan.txt
fatal=$(sed -n '/^## fatal/,/^## listed/p' ../scan.txt | grep -vc '^##' || true)
review=$(sed -n '/^## listed/,$p' ../scan.txt | grep -vc '^##' || true)
[ "$fatal" -eq 0 ] || { sed -n '/^## fatal/,/^## listed/p' ../scan.txt | head -40; die "scan: $fatal fatal hits (see $R/scan.txt)"; }
note "scan: 0 sorry/admit/axiom/native_decide/implemented_by/local RiemannHypothesis; $review lines listed for review in $R/scan.txt"

# --- 3. Pins.
[ "$(cat lean-toolchain)" = "leanprover/lean4:v4.33.1" ] || die "lean-toolchain is $(cat lean-toolchain)"
grep -q '"rev": *"0df444a360eaa60ab8c11dca51a86af692955474"' lake-manifest.json || die "Mathlib is not pinned to 0df444a3 in lake-manifest.json"
grep -q 'github.com/leanprover-community/mathlib4' lake-manifest.json || die "Mathlib is not from leanprover-community"
note "pins: Lean v4.33.1, Mathlib 0df444a360eaa60ab8c11dca51a86af692955474 from leanprover-community"

# --- 4. Mathlib's prebuilt cache.
timed "lake exe cache get" ../cache-get.log lake exe cache get

# --- 5. Build. The janitor deletes each module's .setup.json once the module is built (about 1 TB
# otherwise, per the leanprover-community verification notes), and logs progress and disk use.
(
  while sleep 300; do
    find .lake/build/ir -name '*.setup.json' 2>/dev/null | while IFS= read -r f; do
      t=".lake/build/lib/lean/${f#.lake/build/ir/}"
      [ "${t%.setup.json}.trace" -nt "$f" ] && rm -f "$f"
    done
    echo "progress: $(grep -oE '^. \[[0-9]+/[0-9]+\]' ../build.log 2>/dev/null | tail -1); disk used in $R: $(du -sh "$R" 2>/dev/null | cut -f1); free on /: $(df -h --output=avail / | tail -1)"
  done
) &
janitor=$!

timed "lake build Statements" ../build-statements.log lake build Statements
timed "lake build Main" ../build.log lake build Main
note "  $(grep -E 'Build completed successfully' ../build.log | tail -1)"

# --- 6. Axioms of the five theorems, and the statement RiemannHypothesis resolves to.
cat >../Check.lean <<'EOF'
import Main
#print axioms riemannHypothesis
#print axioms riemannZeta_eq_zero_re
#print axioms LFunction_ne_zero_of_half_lt_re
#print axioms LFunction_eq_zero_re
#print axioms LFunction_eq_zero_re_of_isPrimitive
#check @riemannHypothesis
#print RiemannHypothesis
EOF
timed "axiom check" ../axioms.log lake env lean ../Check.lean
for t in "${theorems[@]}"; do
  grep -qF "'$t' depends on axioms: [propext, Classical.choice, Quot.sound]" ../axioms.log \
    || { cat ../axioms.log; die "$t: axioms are not exactly propext, Classical.choice, Quot.sound"; }
  note "  ok: $t depends only on propext, Classical.choice, Quot.sound"
done
[ "$(grep -c 'depends on axioms' ../axioms.log)" -eq 5 ] || die "unexpected extra axiom lines in axioms.log"
note "RiemannHypothesis, as Lean prints it (compare with Mathlib/NumberTheory/LSeries/RiemannZeta.lean):"
sed -n '/^riemannHypothesis :/,$p' ../axioms.log | sed 's/^/    /' | tee -a summary.txt >/dev/null

# --- 7. Replay every declaration of Main and its imports through the kernel, from the compiled files.
lc="$(dirname "$(elan which lean)")/leanchecker"
[ -x "$lc" ] || die "no leanchecker in the v4.33.1 toolchain ($lc)"
timed "leanchecker --fresh Main" ../leanchecker.log lake env "$lc" --fresh Main

done=yes
