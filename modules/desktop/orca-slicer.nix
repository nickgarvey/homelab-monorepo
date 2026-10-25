# OrcaSlicer, patched for its GStreamer, glew/EGL and NULL-deref crashes.

{ config, lib, pkgs, ... }:

{
  environment.systemPackages = with pkgs; [
    ((orca-slicer.override {
      # Upstream:      nixpkgs pkgs/by-name/or/orca-slicer/package.nix
      # Upstreamable:  yes
      # How:           nixpkgs PR building orca-slicer against
      #                glew.override { enableEGL = false; }. NixOS/nixpkgs#531346
      #                did this and was closed unmerged; reopen or resubmit it.
      # Written:       2026-06-18, against orca-slicer 2.3.1
      # Last checked:  2026-10-25: nixpkgs' orca-slicer 2.4.2 still takes the
      #                default glew, and glew defaults enableEGL to true on Linux
      # Remove when:   nixpkgs' orca-slicer builds against a glew without EGL
      #
      # orca's wxGLCanvas uses a GLX context. GLEW built for EGL cannot read the
      # GL version from it ("Unable to init glew library, Error: Missing GL
      # version"), so the 3D build plate never renders and the app crashes.
      glew = glew.override { enableEGL = false; };
    }).overrideAttrs (old: {
      patches = (old.patches or []) ++ [
        ../../patches/orca-slicer-null-checks.patch
      ];
      # Upstream:      nixpkgs pkgs/by-name/or/orca-slicer/package.nix (preFixup)
      # Upstreamable:  yes
      # How:           nixpkgs PR adding this --set GST_PLUGIN_SCANNER line to the
      #                package's own gappsWrapperArgs. No PR filed yet. Other
      #                GStreamer users wrapped by wrapGAppsHook3 may have the same
      #                bug; the hook does not set GST_PLUGIN_SCANNER.
      # Written:       2026-05-24, against orca-slicer 2.3.2
      # Last checked:  2026-10-25: nixpkgs' orca-slicer 2.4.2 still sets only
      #                GST_PLUGIN_SYSTEM_PATH_1_0
      # Remove when:   nixpkgs' orca-slicer wrapper sets GST_PLUGIN_SCANNER
      #
      # Without gst-plugin-scanner, GStreamer builds a near-empty registry (~7 KB
      # instead of ~1 MB) with no playbin, and wxMediaCtrl2 segfaults building
      # the Monitor (printer camera) tab, at startup or on switching to it. To
      # reproduce, delete ~/.cache/gstreamer-1.0/registry.x86_64.bin and start
      # orca-slicer.
      preFixup = (old.preFixup or "") + ''
        gappsWrapperArgs+=(
          --set GST_PLUGIN_SCANNER "${gst_all_1.gstreamer}/libexec/gstreamer-1.0/gst-plugin-scanner"
        )
      '';
    }))
  ];
}
