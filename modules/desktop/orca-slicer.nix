# OrcaSlicer, patched for its GStreamer, glew/EGL and NULL-deref crashes.

{ config, lib, pkgs, ... }:

{
  environment.systemPackages = with pkgs; [
    ((orca-slicer.override {
      # orca's wxGLCanvas uses a GLX context. GLEW built for EGL (nixpkgs'
      # default on Linux) cannot read the GL version from it ("Unable to init glew
      # library, Error: Missing GL version"), so the 3D build plate never renders
      # and the app crashes. NixOS/nixpkgs#531346 did this upstream and was
      # closed unmerged.
      glew = glew.override { enableEGL = false; };
    }).overrideAttrs (old: {
      patches = (old.patches or []) ++ [
        ../../patches/orca-slicer-null-checks.patch
      ];
      # nixpkgs' wrapper sets GST_PLUGIN_SYSTEM_PATH_1_0 but not GST_PLUGIN_SCANNER
      # (wrapGAppsHook3 does not add it). Without gst-plugin-scanner, GStreamer
      # builds a near-empty registry (~7 KB instead of ~1 MB) with no playbin, and
      # wxMediaCtrl2 segfaults building the Monitor (printer camera) tab, at
      # startup or on switching to it. To reproduce, delete
      # ~/.cache/gstreamer-1.0/registry.x86_64.bin and start orca-slicer.
      preFixup = (old.preFixup or "") + ''
        gappsWrapperArgs+=(
          --set GST_PLUGIN_SCANNER "${gst_all_1.gstreamer}/libexec/gstreamer-1.0/gst-plugin-scanner"
        )
      '';
    }))
  ];
}
