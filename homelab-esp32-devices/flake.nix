# ESP32 development shell with ESP-IDF and OpenOCD.
# See docs/esp-prog-2.md for ESP-Prog-2 usage and udev setup.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    nixpkgs-esp-dev.url = "github:mirrexagon/nixpkgs-esp-dev";
  };

  outputs = { self, nixpkgs, nixpkgs-esp-dev }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs {
      inherit system;
      overlays = [ nixpkgs-esp-dev.overlays.default ];
      config.permittedInsecurePackages = [ "python3.13-ecdsa-0.19.1" ];
    };

    # Source filter for firmware builds: strip build artifacts, downloaded
    # components, and repo-level directories that are not compiled into firmware
    # (scripts/, tests/, docs/).  Excluding these means edits to flash.py or
    # test files don't invalidate the firmware derivation cache.
    cleanSrc = root: pkgs.lib.cleanSourceWith {
      src = root;
      filter = name: type:
        let
          base = builtins.baseNameOf name;
          rel  = pkgs.lib.removePrefix (toString root + "/") name;
        in
        !(builtins.elem base [
          "build" "managed_components" ".git" "sdkconfig" "result"
        ]) &&
        !(builtins.elem (pkgs.lib.head (pkgs.lib.splitString "/" rel)) [
          "scripts" "tests" "docs"
        ]);
    };

    # --------------------------------------------------------------------------
    # freezer-temp-sensor
    # --------------------------------------------------------------------------

    # Fixed-output derivation: downloads all managed_components via idf.py.
    # Network access is allowed in FODs; Nix verifies the entire output NAR hash.
    # To update this hash after changing dependencies.lock:
    #   ./scripts/update-components-hash.py
    freezerComponents = pkgs.stdenv.mkDerivation {
      name = "freezer-temp-sensor-idf-components";

      # Include device source + shared components (CMakeLists.txt references ../../components)
      src = cleanSrc self;

      outputHashAlgo = "sha256";
      outputHashMode = "recursive";
      outputHash = "sha256-NtnEXDg0d9tkKMDxEnrNqhxJcutppejjz4WbGvxqr2E="; # freezer-components

      nativeBuildInputs = [ pkgs.esp-idf-full ];

      phases = [ "unpackPhase" "buildPhase" ];

      buildPhase = ''
        cd devices/freezer-temp-sensor

        # Component manager needs a writable cache dir; Nix sandbox has no real $HOME
        export HOME=$TMPDIR
        export IDF_COMPONENT_MANAGER_CACHE_DIR=$TMPDIR/idf-component-cache

        # Run cmake configure which invokes the IDF component manager download.
        # We only need the managed_components to be populated; reconfigure may
        # fail after downloading them — that is fine.
        idf.py reconfigure || true

        # Verify components were actually downloaded
        if [ ! -d managed_components ]; then
          echo "ERROR: managed_components not created by idf.py reconfigure"
          exit 1
        fi

        cp -r managed_components $out
      '';
    };

    # Firmware build derivation — fully sandboxed (no network).
    #
    # useFakeSensor: when true, appends CONFIG_USE_FAKE_TEMP_SENSOR=y to
    # sdkconfig.defaults before building, selecting the sine-wave sensor path.
    makeFreezerFirmware = { useFakeSensor ? false }: pkgs.stdenv.mkDerivation {
      name = if useFakeSensor
             then "freezer-temp-sensor-fake-firmware"
             else "freezer-temp-sensor-firmware";
      # Full repo needed: CMakeLists.txt references ../../components
      src = cleanSrc self;

      nativeBuildInputs = [ pkgs.esp-idf-full pkgs.cmake pkgs.ninja ];

      # Drive idf.py directly instead of stdenv's auto cmake phase
      dontConfigure = true;

      postUnpack = ''
        # Inject pre-fetched components — prevents re-download in sandbox
        cp -r ${freezerComponents} $sourceRoot/devices/freezer-temp-sensor/managed_components
        chmod -R u+w $sourceRoot/devices/freezer-temp-sensor/managed_components

        # Patch upstream bug in esp_matter 1.4.2: GenericOverallCurrentState and
        # GenericOverallTargetState only define operator==(BaseType&), not
        # operator==(Self&).  GCC 14.2 requires the latter for
        # Nullable<T>::operator== (called from closure-control-cluster-logic.cpp).
        # Add minimal self-comparison operators that delegate to the existing one.
        CLOSURE_H="$sourceRoot/devices/freezer-temp-sensor/managed_components/espressif__esp_matter/connectedhomeip/connectedhomeip/src/app/clusters/closure-control-server/closure-control-cluster-objects.h"
        sed -i 's|bool operator==(const Structs::OverallCurrentStateStruct::Type \& rhs) const|bool operator==(const GenericOverallCurrentState \& rhs) const { return operator==(static_cast<const Structs::OverallCurrentStateStruct::Type \&>(rhs)); }\n    bool operator==(const Structs::OverallCurrentStateStruct::Type \& rhs) const|' "$CLOSURE_H"
        sed -i 's|bool operator==(const Structs::OverallTargetStateStruct::Type \& rhs) const|bool operator==(const GenericOverallTargetState \& rhs) const { return operator==(static_cast<const Structs::OverallTargetStateStruct::Type \&>(rhs)); }\n    bool operator==(const Structs::OverallTargetStateStruct::Type \& rhs) const|' "$CLOSURE_H"
      '';

      buildPhase = ''
        export HOME=$TMPDIR

        # Pre-populate the IDF component manager cache from the Nix-fetched
        # components. Cache format: {namespace}__{name}_{version}_{hash[:8]}/
        # The component manager checks the cache before downloading; if hit, no
        # network access occurs. This makes reconfigure work in the sandbox.
        CACHE_DIR="$TMPDIR/.cache/Espressif/ComponentManager"
        mkdir -p "$CACHE_DIR"
        for src_dir in ${freezerComponents}/*/; do
          name=$(basename "$src_dir")
          version=$(grep -m1 '^version:' "$src_dir/idf_component.yml" \
                    | sed 's/.*version: *//; s/"//g; s/[[:space:]]//g')
          hash=$(cat "$src_dir/.component_hash")
          cache_name="''${name}_''${version}_''${hash:0:8}"
          cp -r "$src_dir" "$CACHE_DIR/$cache_name"
        done
        echo "Component cache pre-populated ($(ls $CACHE_DIR | wc -l) components)"

        ${pkgs.lib.optionalString useFakeSensor ''
          printf '\nCONFIG_USE_FAKE_TEMP_SENSOR=y\n' >> devices/freezer-temp-sensor/sdkconfig.defaults
        ''}

        idf.py -C devices/freezer-temp-sensor reconfigure
        idf.py -C devices/freezer-temp-sensor build
      '';

      installPhase = ''
        mkdir -p $out/bootloader $out/partition_table
        cp devices/freezer-temp-sensor/build/freezer_temp_sensor.bin $out/
        cp devices/freezer-temp-sensor/build/freezer_temp_sensor.elf $out/
        cp devices/freezer-temp-sensor/build/bootloader/bootloader.bin $out/bootloader/
        cp devices/freezer-temp-sensor/build/partition_table/partition-table.bin $out/partition_table/
        cp devices/freezer-temp-sensor/build/flash_args $out/ 2>/dev/null || true
        cp devices/freezer-temp-sensor/build/flasher_args.json $out/ 2>/dev/null || true
      '';
    };

    freezerFirmware     = makeFreezerFirmware {};
    freezerFirmwareFake = makeFreezerFirmware { useFakeSensor = true; };

    # --------------------------------------------------------------------------
    # garage-opener
    # --------------------------------------------------------------------------

    garageComponents = pkgs.stdenv.mkDerivation {
      name = "garage-opener-idf-components";
      src = cleanSrc self;
      outputHashAlgo = "sha256";
      outputHashMode = "recursive";
      outputHash = "sha256-laTtLy+4bl5QV84lXNCQ2cgr1xT76hJCi0LKdQwvEgM="; # garage-components
      nativeBuildInputs = [ pkgs.esp-idf-full ];
      phases = [ "unpackPhase" "buildPhase" ];
      buildPhase = ''
        cd devices/garage-opener
        export HOME=$TMPDIR
        export IDF_COMPONENT_MANAGER_CACHE_DIR=$TMPDIR/idf-component-cache
        idf.py reconfigure || true
        if [ ! -d managed_components ]; then
          echo "ERROR: managed_components not created by idf.py reconfigure"
          exit 1
        fi
        cp -r managed_components $out
      '';
    };

    garageFirmware = pkgs.stdenv.mkDerivation {
      name = "garage-opener-firmware";
      src = cleanSrc self;
      nativeBuildInputs = [ pkgs.esp-idf-full pkgs.cmake pkgs.ninja ];

      dontConfigure = true;
      postUnpack = ''
        cp -r ${garageComponents} $sourceRoot/devices/garage-opener/managed_components
        chmod -R u+w $sourceRoot/devices/garage-opener/managed_components
      '';
      buildPhase = ''
        export HOME=$TMPDIR
        CACHE_DIR="$TMPDIR/.cache/Espressif/ComponentManager"
        mkdir -p "$CACHE_DIR"
        for src_dir in ${garageComponents}/*/; do
          name=$(basename "$src_dir")
          version=$(grep -m1 '^version:' "$src_dir/idf_component.yml" \
                    | sed 's/.*version: *//; s/"//g; s/[[:space:]]//g')
          hash=$(cat "$src_dir/.component_hash")
          cache_name="''${name}_''${version}_''${hash:0:8}"
          cp -r "$src_dir" "$CACHE_DIR/$cache_name"
        done
        echo "Component cache pre-populated ($(ls $CACHE_DIR | wc -l) components)"
        idf.py -C devices/garage-opener reconfigure
        idf.py -C devices/garage-opener build
      '';
      installPhase = ''
        mkdir -p $out/bootloader $out/partition_table
        cp devices/garage-opener/build/garage_door_opener.bin $out/
        cp devices/garage-opener/build/garage_door_opener.elf $out/
        cp devices/garage-opener/build/bootloader/bootloader.bin $out/bootloader/
        cp devices/garage-opener/build/partition_table/partition-table.bin $out/partition_table/
        cp devices/garage-opener/build/flash_args $out/ 2>/dev/null || true
        cp devices/garage-opener/build/flasher_args.json $out/ 2>/dev/null || true
      '';
    };

    # --------------------------------------------------------------------------
    # Unit tests (native, host gcc)
    # --------------------------------------------------------------------------

    # Test source includes tests/ and components/ but excludes build artifacts.
    testSrc = pkgs.lib.cleanSourceWith {
      src = self;
      filter = name: type:
        let
          base = builtins.baseNameOf name;
          rel  = pkgs.lib.removePrefix (toString self + "/") name;
          top  = pkgs.lib.head (pkgs.lib.splitString "/" rel);
        in
        !(builtins.elem base [
          "build" "managed_components" ".git" "sdkconfig" "result"
        ]) &&
        builtins.elem top [
          "tests" "components" "devices"
        ];
    };

    unity = pkgs.fetchFromGitHub {
      owner = "ThrowTheSwitch";
      repo = "Unity";
      rev = "v2.6.0";
      hash = "sha256-SCcUGNN/UJlu3ALJiZ9bQKxYRZey3cm9QG+NOehp6Ow=";
    };

    tests = pkgs.stdenv.mkDerivation {
      name = "homelab-esp32-tests";
      src = testSrc;
      nativeBuildInputs = [ pkgs.cmake pkgs.ninja ];

      cmakeDir = "tests";

      configurePhase = ''
        cmake -S tests -B build -G Ninja \
          -DUNITY_SOURCE_DIR=${unity}
      '';

      buildPhase = ''
        ninja -C build
      '';

      # Run tests as the install step — if they fail, the derivation fails.
      installPhase = ''
        ./build/run_tests
        mkdir -p $out
        cp ./build/run_tests $out/
      '';
    };

    # Helper: build verification check for a firmware derivation.
    # Validates binary size, esptool image header, and expected ELF symbols.
    mkFirmwareCheck = { name, firmware, binName, chip, nmTool, symbols }: pkgs.stdenv.mkDerivation {
      name = "check-${name}";
      dontUnpack = true;
      dontConfigure = true;
      nativeBuildInputs = [ pkgs.esp-idf-full ];
      buildPhase = ''
        PASS=0; FAIL=0
        pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
        fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }

        BINARY="${firmware}/${binName}.bin"
        ELF="${firmware}/${binName}.elf"

        # Binary existence
        if [ -f "$BINARY" ]; then pass "binary exists"
        else fail "binary not found: $BINARY"; fi

        # Size check (>128 KiB)
        SIZE=$(stat -c%s "$BINARY")
        if [ "$SIZE" -ge 131072 ]; then pass "binary size $SIZE >= 128K"
        else fail "binary size $SIZE too small"; fi

        # esptool image validation
        if esptool.py --chip ${chip} image_info "$BINARY" >/dev/null 2>&1; then
          pass "esptool image_info valid"
        else fail "esptool image_info failed"; fi

        # ELF symbol checks
        for sym in ${builtins.concatStringsSep " " symbols}; do
          if ${nmTool} --demangle "$ELF" 2>/dev/null | grep -q " [Tt] $sym"; then
            pass "symbol: $sym"
          else fail "missing symbol: $sym"; fi
        done

        echo ""
        echo "$PASS passed, $FAIL failed"
        [ "$FAIL" -eq 0 ] || exit 1
      '';
      installPhase = "mkdir -p $out && echo ok > $out/result";
    };

    checkFreezer = mkFirmwareCheck {
      name = "freezer-temp-sensor";
      firmware = freezerFirmware;
      binName = "freezer_temp_sensor";
      chip = "esp32c6";
      nmTool = "riscv32-esp-elf-nm";
      symbols = [
        "app_main"
        "ds18b20_reader_read"
        "ds18b20_get_temperature"
        "onewire_bus_reset"
        "MatterTemperatureMeasurementPluginServerInitCallback"
      ];
    };

    checkGarage = mkFirmwareCheck {
      name = "garage-opener";
      firmware = garageFirmware;
      binName = "garage_door_opener";
      chip = "esp32s2";
      nmTool = "xtensa-esp32s2-elf-nm";
      symbols = [
        "app_main"
        "wifi_manager_init"
        "ha_client_init"
        "neopixel_init"
        "neopixel_set"
        "wifi_manager_connected"
      ];
    };

  in {
    devShells.${system}.default = pkgs.mkShell {
      buildInputs = [
        pkgs.esp-idf-full
        pkgs.gcc
        pkgs.cmake
        pkgs.ninja
        pkgs.sops
        pkgs.age
      ];
    };

    packages.${system} = {
      freezer-temp-sensor      = freezerFirmware;
      freezer-temp-sensor-fake = freezerFirmwareFake;
      freezer-temp-sensor-components = freezerComponents;
      garage-opener = garageFirmware;
      garage-opener-components = garageComponents;
      inherit tests;
    };

    checks.${system} = {
      unit-tests = tests;
      freezer-temp-sensor = checkFreezer;
      garage-opener = checkGarage;
    };
  };
}
