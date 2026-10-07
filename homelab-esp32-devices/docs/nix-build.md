# Nix Build System

Firmware is built via Nix derivations defined in `flake.nix`. Builds run on a remote builder and are cached.

---

## How It Works

1. `nix build .#<package>` copies the filtered source tree to the Nix sandbox
2. Inside the sandbox, `idf.py` runs cmake + ninja to compile the ESP-IDF firmware
3. The `installPhase` copies `.bin`, `.elf`, `flasher_args.json`, and partition table to `$out`
4. The output is a read-only Nix store path that `scripts/flash.py` can flash directly

---

## Source Filtering (`cleanSrc`)

The `cleanSrc` function in `flake.nix` controls which files are included in the firmware derivation's source hash. Changes to excluded files do **not** trigger rebuilds.

**Excluded:**
- `scripts/`, `tests/`, `docs/` — development tooling, not firmware source
- `build/`, `managed_components/`, `.git`, `sdkconfig`, `result` — build artifacts

**Important:** New source files must be `git add`-ed before `nix build` will see them. Nix copies the working tree to the store, but untracked files in a dirty tree may not be included when building on a remote builder.

---

## Adding a New Device

1. Create `devices/<name>/` with `main/`, `CMakeLists.txt`, `sdkconfig.defaults`
2. Add a derivation in `flake.nix` (follow the `makeFreezerFirmware` pattern if variants are needed)
3. Add the package to `packages` in the flake outputs
4. Add a build test in `tests/test_build_<name>.sh`

---

## Build Variants

Use a Nix function with parameters to produce variants without code changes:

```nix
makeFreezerFirmware = { useFakeSensor ? false }: pkgs.stdenv.mkDerivation {
  name = if useFakeSensor then "...-fake-firmware" else "...-firmware";
  buildPhase = ''
    ${pkgs.lib.optionalString useFakeSensor ''
      printf '\nCONFIG_USE_FAKE_TEMP_SENSOR=y\n' >> sdkconfig.defaults
    ''}
    idf.py build
  '';
};
```

This appends Kconfig overrides to `sdkconfig.defaults` at build time, producing separate cached derivations for each variant.

---

## Debugging Build Failures

```bash
# See full build log for a failed derivation
nix log /nix/store/<hash>-<name>.drv

# Or build with verbose output
nix build .#freezer-temp-sensor -L
```

Common issues:
- **"Cannot find source file"** — new file not `git add`-ed (see Source Filtering above)
- **"no free tx channels"** at runtime — hardware resource leak; see ds18b20_reader.c for the init-once pattern
- **30-minute rebuild after editing flash.py** — file is inside `cleanSrc` exclusion? Check the filter in `flake.nix`
