#!/usr/bin/env bash
# Runs inside the container with the VCMI source at /src and a persistent home at /work.
set -euo pipefail
export HOME=/work/home CONAN_HOME=/work/conan RUNNER_TEMP=/work/tmp
export GRADLE_USER_HOME=/work/home/.gradle
mkdir -p "$GRADLE_USER_HOME" "$RUNNER_TEMP"
# overrides the obsolete value Qt's androiddeployqt writes into the generated project
echo "android.bundle.enableUncompressedNativeLibs=true" > "$GRADLE_USER_HOME/gradle.properties"
cd /src

if [ ! -f /work/.deps-restored ]; then
  conan profile detect --force
  bash CI/install_conan_dependencies.sh dependencies-android-arm64-v8a
  touch /work/.deps-restored
fi

conan install . \
  --output-folder=conan-generated \
  --build=never \
  --profile=dependencies/conan_profiles/android-64-ndk \
  --conf="tools.cmake.cmaketoolchain:generator=Ninja" \
  --profile=dependencies/conan_profiles/base/android-system

cmake --preset android-conan-ninja-release \
  "-DANDROID_GRADLE_PROPERTIES=applicationIdSuffix=.dmg;signingConfig=dailySigning;applicationLabel=VCMI DMG;applicationVariant=daily"
cmake --build --preset android-conan-ninja-release

out=out/build/android-conan-ninja-release/android-build/vcmi-app/build/outputs/apk/release
ls -la "$out"
