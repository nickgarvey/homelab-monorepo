# Verifying Alpöge's Lean proof of the Riemann Hypothesis in the homelab

Notes from checking Levent Alpöge's Lean 4 proof of the Riemann Hypothesis (and GRH for
Dirichlet L-functions), released 2026-11-20. It was checked twice, in two independent ways, on
2026-11-22/23:

| | talos: independent checkers | wabbajack: full build from source |
|---|---|---|
| Input | Andrew Sutherland's lean4export of the proof | Alpöge's source archive |
| Checks | con-leche and con-ron (not Lean's kernel) | `lake build Main`, `#print axioms`, `leanchecker --fresh Main` (Lean's kernel) |
| Result | PASS | PASS |
| Wall time | about 28 min (first run) | 24 h 12 min |
| Code | `packages/riemann-proof/outputs.nix` on dev (`nix run .#riemann-proof`) | `packages/riemann-proof/full.sh` on branch `riemann-full` |

## The proof

- 175,187 Lean 4 files, about 17.4M lines, Lean v4.33.1, Mathlib `0df444a360eaa60ab8c11dca51a86af692955474`.
- Archive: `https://alpo.ge/lean.tar.gz.part01` … `part14`, joined to `lean.tar.gz`
  (239,275,554 bytes, sha256 `9631b4f020199e580ba86178cb56c6e9b09f690c2369a80dc4334aa93f49c7fc`).
  The server answers 200 with an HTML page for any path, so only the joined hash means anything.
- `src/Main.lean` states five theorems: `riemannHypothesis : RiemannHypothesis` (Mathlib's
  definition) and four GRH statements: `riemannZeta_eq_zero_re`,
  `LFunction_ne_zero_of_half_lt_re`, `LFunction_eq_zero_re`, and
  `LFunction_eq_zero_re_of_isPrimitive`.
- Lean prints the statement as
  `∀ (s : ℂ), riemannZeta s = 0 → (¬∃ n, s = -2 * (↑n + 1)) → s ≠ 1 → s.re = 1 / 2`.

## Check 1: independent checkers on talos

`nix run .#riemann-proof` builds the derivation `riemann-proof-check` and prints its RESULT. The
derivation only builds if every check passes, so a successful build is the verdict. It has
`preferLocalBuild = true` and `allowSubstitutes = false`, so it always runs on the local machine
and is never taken from celler.

Inputs, from https://math.mit.edu/~drew/grh/:

| File | Compressed sha256 | Unpacked sha256 |
|---|---|---|
| `solution.ndjson.zst` (7.7 GB; 62 GB unpacked): the export of module `Main` | `61a8938a…5381` | `5ea74b91…244e` |
| `challenge.ndjson.zst` (47 MB): the five statements, proved by `sorry`, importing only Mathlib | `c24ef99d…8ac6` (computed here; not published) | `bd484ac9…b2b9` |

Checkers, each pinned and built as a derivation:
- con-leche, a checker with a proof of its own consistency:
  - `master` at `cd2eceab` checks every declaration.
  - `compare` at `5dc36dc1` checks the solution against the challenge.
  - Both are on the official Lean v4.36.0 release binary; nixpkgs only had 4.35.0.
- con-ron (`3f22699b`): con-leche ported to Rust, a second implementation.

Results on talos (Ryzen 9 9950X3D, 62 GiB), `--jobs=16`:

| Step | Wall | Peak RSS |
|---|---|---|
| con-leche: all 1,468,418 declarations accepted | 434–469 s | 45 GB |
| con-leche `--compare`: five theorems justified, whole solution re-checked | 421–473 s | 45 GB |
| con-ron `--compare`: the same | 499–513 s | 43 GB |

Available RAM never fell below 50 GB. A second `nix run` returns from the local store in well
under a second.

What it took:
- **Inputs:** both `.zst` files came from wabbajack's first download. `nix-store --add-fixed sha256 <file>`
  gives exactly the store path `fetchurl` produces for the same name and hash, so no derivation
  downloaded them again.
- **Celler:** both files were pushed to celler. The first push of the 7.7 GB file failed with
  HTTP 502: cellerd was OOM-killed at its 2 GiB limit while two pushes of the same path ran at
  once (a manual `celler push`, plus talos's `celler-watch-store`). The limit is now 4 GiB
  (`k8s/manifests/celler/deployment.yaml`). Celler splits uploads into chunks averaging
  64 KiB, so 7.7 GB is about 117,000 objects in RGW. One upload alone runs at about 22 MiB/s.
- **Lean for con-leche:** the release tarball with `autoPatchelfHook`. con-leche's lakefile has
  no dependencies, so `lake build con-leche` works in the sandbox.
- **Rust:** con-ron builds with `buildRustPackage` and its `Cargo.lock`. The `vendor/aeneas`
  submodule is not needed.

## Check 2: full build from source on wabbajack

`packages/riemann-proof/full.sh` (branch `riemann-full`) runs as a transient systemd unit. Every
file lives under `~/riemann`, and `HOME` is pointed at `~/riemann/home` so that elan, Mathlib's
cache and git's config stay inside. The start command is in the script's header.

Host: wabbajack, Ryzen AI Max+ 395 (16 cores, 32 threads), 125 GiB RAM, no swap, NVMe `/`.

Unit settings and why:
- `MemoryMax=110G`: leaves room for garage and the host.
- `LimitNOFILE=524288`: at the default of 1,024, `lake exe cache get` fails to unpack thousands
  of Mathlib files (reported by others).
- `Nice=10`, `CPUWeight=idle`, `AllowedCPUs=0-14,16-30`: CPUs 15 and 31 (one physical core,
  per `thread_siblings_list`) stay free for the host. These were applied live with
  `systemctl set-property --runtime`, without a restart.
- `vm.max_map_count` was already 1048576. At the kernel default of 65530, one other build failed
  hundreds of modules: Lean maps every imported `.olean`, and `Main` imports 186,055 modules.

Steps and results:

| Step | Result | Time | Largest process |
|---|---|---|---|
| Download and hash | matches `9631b4f0…` | seconds | |
| Scan `src/` | 0 `sorry`, `admit`, `axiom`, `native_decide`, `implemented_by`, or local `RiemannHypothesis`; 11 lines for review (below) | seconds | |
| Pins | Lean v4.33.1; Mathlib `0df444a3` from leanprover-community | | |
| `lake exe cache get` | exit 0 | about 1 min | 1 GB |
| `lake build Statements` | exit 0 | under a minute | 7 GB |
| `lake build Main` | exit 0, "Build completed successfully (183893 jobs)" | about 14.6 h | 58 GB |
| `#print axioms` on the five theorems | each depends only on `propext`, `Classical.choice`, `Quot.sound` | about 1 min | 50 GB |
| `leanchecker --fresh Main` | exit 0 | 9 h 35 min | 106.5 GB RSS, mostly mapped `.olean` files |

Total: 24 h 12 min wall, about 434 CPU-hours, 1 TB written to disk.

The 11 lines listed for review, none of which bears on soundness:
- 5 × `run_cmd … nameDenyListExt.addEntry` in `src/BuildAid/D1–D5.lean`: hides name prefixes
  from library-suggestion tactics.
- 2 × `addDecl` in `CutCptCopy.lean`: copies declarations under new names, still through the
  kernel, and the replay re-checks them.
- 4 × `set_option maxHeartbeats 0`: no elaboration timeout for one declaration each.

### `.setup.json` files and disk

Lake writes a `.setup.json` of about 6 MB per module under `.lake/build/ir/`; uncleaned, they
total about 1 TB. A janitor deletes each one once the module's `.trace` under
`.lake/build/lib/lean/` is newer, following the leanprover-community verification notes.

The first janitor ran in a subshell under `set -e` and `pipefail`, and died silently about 2 h
in. A pass whose last file was not yet built ended its loop with a false `[ … ] && rm`. By the
time anyone noticed, `ir/` held 476 GB and `/` had 114 GB left, falling by about 80 GB/h. A
separate `riemann-janitor` unit took over, and its first pass deleted 78,458 files, freeing
about 470 GB. Two things that surfaced on the way:
- A transient system unit's PATH lacks the usual tools. The first attempt at `riemann-janitor`
  deleted nothing until `PATH=/run/current-system/sw/bin` was set.
- Do not replace a script file that bash is running. Bash reads it as it goes, so the fix was
  committed but not copied to wabbajack until the run ended.

### The replay

`leanchecker --fresh Main` (`src/LeanChecker.lean` and `src/Lean/Replay.lean` in Lean v4.33.1):
1. It imports `Main` and every module it depends on (project, Mathlib, `Init`, `Std`) by
   memory-mapping their `.olean` files. Importing checks nothing.
2. It then replays every constant into an empty environment. Each constant's dependencies are
   added first, then the constant goes to the kernel. Unsafe and partial constants are skipped.
3. For inductive types, the kernel generates the constructors and recursors itself. The stored
   ones are compared against them at the end.
4. Its own docstring calls it a tool to detect "environment hacking": declarations that reached
   the `.olean` files without the kernel. It uses the same kernel, so it does not guard against
   kernel bugs. The independent checkers on talos cover that.

Why it took 9.5 h on one core:
- `--fresh` is one depth-first walk over a single mutable state, so it uses one thread. It also
  prints nothing until it finishes.
- Without `--fresh`, leanchecker checks each module in parallel against its imports as loaded,
  but by default only modules named after the project. That is faster but weaker.
- con-leche did comparable work in about 1.6 CPU-hours, spread over 16 workers.

Memory during the replay:
- RSS grew to 106.5 GB, but its anonymous memory stayed about 9 GB. The rest was mapped
  `.olean` pages (93.4 GiB of `.olean` files in total), which `free` counts as cache and the
  kernel can drop under the cgroup cap.
- So the cap would have slowed the replay rather than killed it. Swap or zswap would not have
  helped, because file pages are never swapped.
- `/proc/<pid>/smaps` showed every one of the 208,081 mapped files at least partly read, so
  residency gave no usable progress gauge once loading had finished.

## Mistakes and fixes along the way

- **Systemd check stage:** an early version reported PASS when its unit was stopped mid-build,
  because the EXIT trap took the status as success. Fixed before it was replaced by the Nix check.
- **Path bugs in `full.sh`:** it `cd`s into `lean/`, so relative paths sent `summary.txt` and
  `RESULT` to `~/riemann/lean/`. The hours column was blank because `awk` was not in the nix
  shell. Both are fixed on the branch (`2db79a6`).
- **A masked failure:** a `celler push … | tail` hid the push's failure, because the pipeline's
  exit status was `tail`'s.

## Trust

Between them, the two checks establish:
- The formal proof compiles from source.
- It uses only Lean's three standard axioms.
- Lean's kernel accepts every declaration on a clean replay.
- Two checkers independent of Lean's kernel accept Sutherland's export of it, and confirm it
  proves the five statements.

Still trusted:
- Lean's logic: consistent relative to ZFC plus inaccessible cardinals.
- Mathlib's definitions of `riemannZeta` and `RiemannHypothesis`.
- On talos, that `challenge.ndjson` encodes Mathlib's statements. The wabbajack build covers
  this independently, through `#print RiemannHypothesis` on the source build. A byte-for-byte
  regeneration of the challenge is ait `nix-EUkhx.6`.

Neither check reviews the mathematics.

## Sources

- Lean community announcement: https://leanprover-community.github.io/blog/posts/riemann-hypothesis/
- Sutherland's verification files and logs: https://math.mit.edu/~drew/grh/
- leanprover-community verification notes (the janitor, memory estimates):
  https://github.com/leanprover-community/riemann-hypothesis/blob/main/verification/NOTES.md
- Hertaspace build report (`max_map_count`, `ulimit`, two passes):
  https://github.com/Hertaspace/alpoge-rh-lean-verification
- 32-core build report: https://github.com/hhh2210/riemann-lean-account
- Mirror with `Check.lean`: https://github.com/CocoSgt/riemann-hypothesis-lean
- Tracking: ait epic `nix-EUkhx`.

## Cleanup

Everything from the wabbajack run is in `~/riemann` (about 150 GB). The earlier systemd check
left `~/rh-lean` (about 70 GB). Both can be removed with `rm -rf ~/riemann ~/rh-lean`.
