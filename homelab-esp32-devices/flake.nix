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

    # Source filter: strip build artifacts and downloaded components so only
    # committed source is included in the Nix store.
    cleanSrc = root: pkgs.lib.cleanSourceWith {
      src = root;
      filter = name: type:
        let base = builtins.baseNameOf name; in
        !(builtins.elem base [
          "build" "managed_components" ".git" "sdkconfig" "result"
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
        # fail after downloading them (e.g. SOPS secrets unavailable) — that is fine.
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
    # Secrets fall back to placeholders (no SOPS key in sandbox); use flash.py
    # for production firmware with real Thread credentials baked in.
    freezerFirmware = pkgs.stdenv.mkDerivation {
      name = "freezer-temp-sensor-firmware";
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

        idf.py -C devices/freezer-temp-sensor reconfigure
        idf.py -C devices/freezer-temp-sensor build
      '';

      installPhase = ''
        # Mirror the idf.py build output layout so that flash.py can invoke
        # `idf.py -B $out flash` and flasher_args.json relative paths resolve.
        mkdir -p $out/bootloader $out/partition_table
        cp devices/freezer-temp-sensor/build/freezer_temp_sensor.bin $out/
        cp devices/freezer-temp-sensor/build/freezer_temp_sensor.elf $out/
        cp devices/freezer-temp-sensor/build/bootloader/bootloader.bin $out/bootloader/
        cp devices/freezer-temp-sensor/build/partition_table/partition-table.bin $out/partition_table/
        cp devices/freezer-temp-sensor/build/flash_args $out/ 2>/dev/null || true
        cp devices/freezer-temp-sensor/build/flasher_args.json $out/ 2>/dev/null || true
      '';
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
      freezer-temp-sensor = freezerFirmware;
      freezer-temp-sensor-components = freezerComponents;
    };
  };
}
