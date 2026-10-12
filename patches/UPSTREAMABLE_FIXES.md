# Upstreamable Fixes

Local nixpkgs workarounds in this repo that should eventually be PR'd upstream.
Each entry should describe the bug, where it's worked around locally, and what
the upstream fix should look like.

---

## orca-slicer: wrapper missing `GST_PLUGIN_SCANNER`, causing SIGSEGV in Monitor tab

**Affected:** `pkgs/by-name/or/orca-slicer/package.nix` (nixpkgs)
**Local workaround:** `modules/desktop/orca-slicer.nix` — `orca-slicer.overrideAttrs` adds `--set GST_PLUGIN_SCANNER` to `gappsWrapperArgs`.

### Symptom

OrcaSlicer segfaults when the Monitor (printer camera) tab is constructed. On
some launches the crash happens at startup because MainFrame builds the Monitor
panel eagerly; on others it triggers when switching to the tab.

Crash stack (from `coredumpctl` / `gdb`):

```
SIGSEGV in wxMediaCtrl2::wxMediaCtrl2(wxWindow*)
  Slic3r::GUI::StatusBasePanel::create_monitoring_page()
  Slic3r::GUI::StatusBasePanel::StatusBasePanel(...)
  Slic3r::GUI::StatusPanel::StatusPanel(...)
  Slic3r::GUI::MonitorPanel::init_tabpanel()
  Slic3r::GUI::MainFrame::init_tabpanel()
  ...
```

### Root cause

With `GST_DEBUG=2` the preceding GStreamer log line is:

```
WARN GST_ELEMENT_FACTORY no such element factory "playbin"!
```

Disassembly around the faulting instruction shows the constructor calls
`gst_element_factory_make("playbin", ...)`, does not null-check the result, and
then dereferences it via `g_object_set` — `%rax = 0x2c8(%rbx)` is `NULL`.

`playbin` is missing from the GStreamer registry because GStreamer can't find
its plugin-scanner helper at runtime. The current nixpkgs wrapper sets:

- `GST_PLUGIN_SYSTEM_PATH_1_0` — where the plugin `.so`s live ✅
- `GST_PLUGIN_SCANNER` — the path to `gst-plugin-scanner` ❌ (missing)

`gst-plugin-scanner` is the out-of-process helper that introspects each plugin
to populate the registry. Without it, registry generation silently produces a
near-empty cache (observed at ~7 KB vs. ~1 MB after the fix) and most plugins —
including `playbin` from `gst-plugins-base`'s `libgstplayback.so` — are absent.

### Reproduction (clean shell)

```sh
rm -f ~/.cache/gstreamer-1.0/registry.x86_64.bin
orca-slicer    # crashes (Monitor tab or startup)
```

### Verification of fix

```sh
rm -f ~/.cache/gstreamer-1.0/registry.x86_64.bin
GST_PLUGIN_SCANNER=/nix/store/.../gstreamer-1.26.11/libexec/gstreamer-1.0/gst-plugin-scanner \
  orca-slicer
# Registry rebuilds to ~1 MB; Monitor tab opens without crashing.
```

### Suggested upstream patch

In `pkgs/by-name/or/orca-slicer/package.nix`, extend the existing `preFixup`:

```nix
preFixup = ''
  gappsWrapperArgs+=(
    --prefix LD_LIBRARY_PATH : "$out/lib:${lib.makeLibraryPath [ glew ]}"
    --set WEBKIT_DISABLE_COMPOSITING_MODE 1
    --set FONTCONFIG_FILE "${fontsConf}"
    --set GST_PLUGIN_SCANNER "${gst_all_1.gstreamer}/libexec/gstreamer-1.0/gst-plugin-scanner"
    ...
  )
'';
```

Note: `wrapGAppsHook3` does not automatically propagate `GST_PLUGIN_SCANNER`
even when `gst_all_1.gstreamer` is in `buildInputs`. Other GStreamer-using
packages in nixpkgs may have the same latent bug and could be audited.

---

## mainsail: nginx `upstreams` server key not IPv6-safe

**Affected:** `nixos/modules/services/web-apps/mainsail.nix` (nixpkgs)
**Local workaround:** `hosts/skyforge/configuration.nix` overrides `services.nginx.upstreams.mainsail-apiserver.servers` with `lib.mkForce` to a bracketed `[::1]:7125`. Still present in nixpkgs `mainsail.nix`.

### Symptom

When `services.moonraker.address` is an IPv6 literal (e.g. `::1`), the mainsail
module generates an nginx `upstream` server entry like:

```
server ::1:7125;
```

which nginx rejects — IPv6 literals in `server` directives must be bracketed
(`[::1]:7125`). The mainsail vhost then fails to start.

### Root cause

`mainsail.nix` interpolates `moonraker.address` directly into the upstream
server key:

```nix
upstreams.mainsail-apiserver.servers."${moonraker.address}:${toString moonraker.port}" = { };
```

There's no bracketing for IPv6 hosts.

### Patch

Adds a small helper and uses it for the server key:

```diff
 let
   cfg = config.services.mainsail;
   moonraker = config.services.moonraker;
+  escapedHost = host: if lib.hasInfix ":" host then "[${host}]" else host;
 in
 ...
-      upstreams.mainsail-apiserver.servers."${moonraker.address}:${toString moonraker.port}" = { };
+      upstreams.mainsail-apiserver.servers."${escapedHost moonraker.address}:${toString moonraker.port}" = { };
```

### Notes for upstreaming

- The sibling `fluidd.nix` module almost certainly has the same bug — check and
  fix in the same PR.
- `lib.hasInfix ":"` is a reasonable proxy for "is this an IPv6 literal" since
  hostnames and IPv4 addresses can't contain `:`. Alternatively, the helper
  could live in `lib` since this pattern recurs across nginx-using modules.

---

## OrcaSlicer: two NULL-deref crashes (upstream to OrcaSlicer, not nixpkgs)

**Affected:** `SoftFever/OrcaSlicer` — verified against tag `v2.3.2`. nixpkgs now packages `v2.4.2`, which this patch is applied to; the bugs have not been re-checked there.
**Local workaround:** `patches/orca-slicer-null-checks.patch`, applied via `overrideAttrs` in `modules/desktop/orca-slicer.nix`.

### Bug 1: `Plater.cpp:6042` — unchecked `option<ConfigOptionStrings>("filament_colour")`

```cpp
project_filament_count = config_loaded.option<ConfigOptionStrings>("filament_colour")->size();
```

If a loaded 3mf project's parsed config doesn't expose `filament_colour` as a
`ConfigOptionStrings` (e.g. a 3mf produced by Creality Print for a printer
profile the user doesn't have installed locally), `option<...>(name, false)`
returns `nullptr`, and the immediate `->size()` virtual call segfaults on the
vtable load. Reproduced opening a Creality-Print-generated 3mf for an Ender-3
V3 SE without that printer profile installed.

**Status in master:** unchanged as of cloning (commit `c383587a`).

### Bug 2: `wxMediaCtrl2.cpp:45` — unchecked `m_imp` and inner playbin

```cpp
auto playbin = reinterpret_cast<wxGStreamerMediaBackend *>(m_imp)->m_playbin;
g_object_set (G_OBJECT (playbin), "audio-sink", NULL, NULL);
```

If `wxMediaCtrl::Create` can't construct a working GStreamer backend (e.g.
`playbin` element missing from the registry, which happens on NixOS when the
`GST_PLUGIN_SCANNER` env var isn't set — see the orca-slicer nixpkgs entry
above), `m_imp` is `nullptr` and dereferencing it segfaults.

**Status in master:** partially fixed (`m_imp` is now null-checked), but the
inner `playbin` pointer is still dereferenced unconditionally. Our patch
guards both.

### Patch

See `patches/orca-slicer-null-checks.patch`. Both hunks are minimal defensive
null-checks; they don't try to recover or signal the user, just avoid the
crash. A proper upstream fix would probably also surface a UI notification
explaining the missing profile / missing GStreamer plugin.

---

## jellyfin-desktop: NativeShell never reaches the web client, killing input and mpv

**Affected:** `pkgs/by-name/je/jellyfin-desktop/package.nix` (nixpkgs, currently v2.0.0)
**Local workaround:** `modules/desktop/common-tv.nix` — `jellyfin-media-player.overrideAttrs`
pins `src` to upstream commit `cabddc6`, guarded by an assertion that fails the
build once nixpkgs packages anything other than 2.0.0.

### Symptom

On `guevenne`, every keypress and controller button in Jellyfin Media Player
did nothing. The log showed, for each one:

    Input received: source: "Keyboard" keycode: "Down"
    No match for: "Down"
    Web Client has not connected, handling input in host instead.

and mpv never initialised: `vo/libmpv: No render context set`.

### Cause

2.0.0 registers its nativeshell bootstrap script on the WebEngineView
(`web.userScripts`). Startup loads the bundled `qrc://` server picker and then
navigates to the remote web client; that cross-origin navigation moves to a new
renderer process and the view-level registration is not delivered to it. The
web client document ends up with no `window.jmpInfo`, no `window.NativeShell`
and no `QWebChannel`, so the handshake that carries input and playback never
happens. Confirmed via `--remote-debugging-port`: those globals are present on
the first (qrc) document and absent on the web client; completing the handshake
by hand exposes all nine JMP objects, so only the injection is broken.

### Upstream fix

https://github.com/jellyfin/jellyfin-desktop/commit/cabddc6 — one line,
`web.userScripts` -> `web.profile.userScripts`. It postdates the v2.0.0 tag, so
nixpkgs just needs a version bump once upstream tags a release containing it.

### Gotcha when testing this

Qt caches compiled QML under `~/.cache/jellyfin-desktop/qmlcache`, keyed on the
`qrc:` path, which does not change between builds. A stale unit therefore
survives rebuilds and keeps running the old `webview.qml` no matter what you
patch — it cost hours here. The launcher sets `QML_DISABLE_DISK_CACHE=1`. To
spot it, compare the QML line numbers in the log (`@ 275`) against the file you
think you built.

---

## upower: spurious single-cycle zero battery readings

**Affected:** `upower` (freedesktop), `src/linux/up-device-supply-battery.c`.
**Local workaround:** `patches/upower-filter-spurious-zero.patch`, applied by the
overlay in `modules/desktop/upower-overlay.nix` (imported by dovahkiin).

Some embedded controllers (seen on a Framework 13 AMD) report
`charge_now`/`capacity` as 0 for a single polling cycle. upower passes that
straight through, so the desktop fires a "battery critical" warning. The patch
drops a near-zero reading that follows a healthy one, and accepts it on the next
cycle if the kernel is still reporting 0. An upstream fix would likely want this
behind a quirk flag rather than unconditional.

---

## OrcaSlicer: plate thumbnails render in the wrong GL context on Wayland (upstream to OrcaSlicer)

**Affected:** `SoftFever/OrcaSlicer`, `src/slic3r/GUI/GLCanvas3D.cpp` (`render_thumbnail`).
**Local workaround:** `patches/orca-slicer-thumbnail-glcontext.patch`, applied in
`modules/desktop/orca-slicer.nix`.

`GLCanvas3D::render_thumbnail` never calls `_set_current()`. Under Wayland, GDK
makes its own paint context current while drawing the WebKit views, so after
slicing the thumbnail render runs in a context where none of the canvas's
VAOs/VBOs exist, and libnvidia-eglcore segfaults. The patch makes the shared
context current first (and skips the render if that fails). The patch header
has the full trace.

---

## orca-slicer: glew built with EGL can't init from orca's GLX context

**Affected:** `pkgs/by-name/or/orca-slicer/package.nix` (nixpkgs).
**Local workaround:** `modules/desktop/orca-slicer.nix` overrides orca-slicer with
`glew = glew.override { enableEGL = false; }`. Mirrors nixpkgs PR #531346.

nixpkgs builds glew with the EGL backend (`enableEGL` defaults to true on Linux),
but orca's `wxGLCanvas` uses a GLX context. GLEW-EGL can't read the GL version
from it ("Unable to init glew library, Error: Missing GL version"), so the 3D
build plate never renders and the app crashes. Drop the override once PR #531346
(or an equivalent) lands.

---

## object_store: AssumeRoleWithWebIdentity parameters sent in the query string

**Affected:** `object_store` (apache/arrow-rs-object-store) 0.14.2,
`src/aws/credential.rs` `web_identity()`, and Ceph RGW's STS endpoint.
**Local workaround:** `patches/object_store-sts-form-body.patch`, applied to the
vendored crate by `packages/celler/outputs.nix` (celler's S3 backend).

object_store POSTs `Action=AssumeRoleWithWebIdentity&...&WebIdentityToken=...`
as URL query parameters with an empty body. Ceph RGW only routes STS calls whose
parameters are in an `application/x-www-form-urlencoded` body, which is what the
AWS SDKs and aws-cli send; it answers the query-string form with 405 before any
authentication. A side effect is that the JWT ends up in RGW's access log. The
patch sends the same parameters as a form body, enabling the crate's existing
`form()` builder for `aws-base` too. Upstream object_store should send a form
body like the SDKs do (AWS STS accepts both, so this is compatible); arguably
RGW should also accept query parameters.

---

## OpenShell: policy DNS answers AAAA with NODATA, so IPv6-only sandboxes resolve nothing

**Affected:** `NVIDIA/OpenShell` v0.1.3 (and 0.1.2),
`crates/openshell-supervisor-network/src/policy_dns/runtime.rs`, which calls
`handle_udp_query_with_ipv6(..., false)`.
**Local workaround:** `patches/openshell-ipv6-policy-dns.patch`, applied by
`packages/openshell/outputs.nix` to the CLI and to the gateway, supervisor and
sandbox-runtime images that `k8s/vendor/openshell` runs. `agents/outputs.nix`
fails the build after 2026-12-01 while the patch is carried (nix-lNoiG).

A sandbox reaches the network only through its supervisor's policy DNS and
proxy. Policy DNS answers every AAAA query with NODATA and resolves only A
records upstream, so in an IPv6-only cluster every name fails
(`policy_dns_upstream_no_data`). The upstream fix is NVIDIA/OpenShell#3702
("IPv6 policy DNS egress", head `9ab8076a`): a `policy_dns_ipv6_egress` mode
(`auto` answers AAAA when the supervisor's namespace has an IPv6 default route
and no IPv4 one), NAT64-aware SSRF checks, and driver settings to pass both to
the supervisor. As of 2026-10-10 it is open, in conflict, and the maintainers
asked whether it is still needed. The patch is those six commits rebased onto
v0.1.3: `architecture/sandbox.md`, deleted upstream, is dropped, and the docker
and podman drivers keep both v0.1.3's `proxy_ca_bundle` and the PR's
`ipv6_egress`, and the PR's two new `run_sandbox` arguments are carried
through v0.1.3's `SandboxRunConfig`. In the Kubernetes driver's tests, the
PR's `supervisor_pod` test passes v0.1.3's `tracing_environment` argument and
v0.1.3's tracing test passes the PR's `ipv6_egress_args`. Posting the rebase to the PR would
answer the maintainers' question.

---

## OpenShell: the supervisor pod carries none of the sandbox template's labels

**Affected:** `NVIDIA/OpenShell` v0.1.3, the Kubernetes driver with the sandbox
runtime: `crates/openshell-driver-kubernetes/src/sandbox_runtime.rs`
(`supervisor_pod`, whose labels are the fixed `control_labels`) and `driver.rs`
(its create and restart paths).
**Local workaround:** `patches/openshell-supervisor-template-labels.patch`,
applied by `packages/openshell/outputs.nix` after the #3702 patch, to the same
builds. `agents/outputs.nix` fails the build after 2026-12-01 while the patch is
carried (nix-q7Q31).

Each sandbox is two pods: the workload pod, where the agent runs, and the
supervisor pod (`os-supervisor-<sandbox-id>`), whose proxy makes every upstream
connection and which alone gets the SPIFFE Workload API socket for provider
token grants. `SandboxTemplate.labels` reach only the workload pod. The
supervisor pod has only `openshell.ai/*` labels, none of which the caller
controls, so a SPIRE ClusterSPIFFEID cannot put anything the caller chose (here
the agent type, `agents.garvey.sh/type`) into the supervisor's SPIFFE ID, and
no other label selector (NetworkPolicy, monitoring) can tell supervisors apart.

The patch gives `supervisor_pod` the labels of the Sandbox resource's pod
template (read from the CR, so the create and restart paths agree) and puts
them on the supervisor pod under the driver's own: template labels whose
prefix is `openshell.ai` or a subdomain of it are dropped, and the control
labels are applied last, so `boundary-role`, `boundary-pair`, `component` and
`sandbox-id`, which the driver's identity checks and NetworkPolicies select
on, cannot be replaced. Unit tests cover both. Upstream, the same change fits
as is; a caveat for the PR is that a Service or policy selecting on a template
label now matches supervisor pods too.
