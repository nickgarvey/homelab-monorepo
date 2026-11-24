# NInfer inference engine (Qwen3.8-27B, RTX 5090 / sm_120a). Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  # Upstream compiles for sm_120a and nothing else, so there is no point
  # pretending this builds anywhere but an x86_64 host with an RTX 5090.
  supportedSystems = [ "x86_64-linux" ];

  # legacyPackages carries no config, and the CUDA redistributables are
  # unfree — import nixpkgs directly so the build stays pure (no --impure,
  # no NIXPKGS_ALLOW_UNFREE).
  pkgsFor = system: import nixpkgs {
    inherit system;
    config.allowUnfree = true;
  };

  ninferFor = system:
    let
      pkgs = pkgsFor system;
      # 13.1 is the minimum upstream accepts and stays at or under the
      # driver's advertised CUDA version. The default cudaPackages (12.9)
      # is too old.
      cuda = pkgs.cudaPackages_13_1;
    in
    cuda.backendStdenv.mkDerivation {
      pname = "ninfer";
      version = "0-unstable-2026-11-24";

      src = pkgs.fetchFromGitHub {
        owner = "Neroued";
        repo = "ninfer";
        rev = "9ebb72ed756ecbec5dbc18ee22d58fc08575e129";
        hash = "sha256-pJYoDZX/X/yUwuHpRLsfk25SJw79a4Pk+PI8K2qU3uY=";
      };

      nativeBuildInputs = [
        pkgs.cmake
        pkgs.ninja
        pkgs.pkg-config
        cuda.cuda_nvcc
        # Puts /run/opengl-driver/lib on the runpath so the binaries find
        # the host's real libcuda.so.
        pkgs.autoAddDriverRunpath
      ];

      buildInputs = [
        cuda.cuda_cudart
        cuda.cuda_nvtx
        cuda.cuda_cccl
        cuda.cuda_profiler_api
        pkgs.ffmpeg
        pkgs.curl
        pkgs.libjpeg
        pkgs.hwloc
        pkgs.liburing
      ];

      cmakeFlags = [
        "-DNINFER_BUILD_APPS=ON"
        "-DBUILD_TESTING=OFF"
      ];

      # setupCudaHook appends its own -DCMAKE_CUDA_ARCHITECTURES after our
      # cmakeFlags and CMake honours the last -D on the command line, so
      # upstream's "only 120a is supported" guard fires unless the injected
      # value is rewritten rather than merely overridden.
      preConfigure = ''
        cmakeFlags="''${cmakeFlags//-DCMAKE_CUDA_ARCHITECTURES=[^ ]*/}"
        appendToVar cmakeFlags "-DCMAKE_CUDA_ARCHITECTURES=120a"
      '';

      # Upstream ships no install() rules; the build tree is the product.
      # The apps link the engine's shared libraries from lib/ through an
      # $ORIGIN/../lib runpath.
      installPhase = ''
        runHook preInstall
        install -Dm755 apps/ninfer       $out/bin/ninfer
        install -Dm755 apps/ninfer-serve $out/bin/ninfer-serve
        mkdir -p $out/lib
        cp -P lib/*.so* $out/lib/
        runHook postInstall
      '';

      meta = {
        description = "CUDA inference engine for selected Qwen checkpoints on RTX 5090";
        homepage = "https://github.com/Neroued/ninfer";
        license = nixpkgs.lib.licenses.asl20;
        platforms = supportedSystems;
      };
    };

  # The model artifact is 17 GiB and host-specific, so it stays out of the
  # image and is bind-mounted at /models. The CUDA driver is likewise a
  # host concern: the nvidia-container-toolkit CDI device injects the
  # driver libraries at the /run/opengl-driver/lib path the binaries
  # already carry on their runpath.
  #
  #   docker run -d --name ninfer --device nvidia.com/gpu=all \
  #     -v /path/to/qwen3_8_27b.ninfer:/models/model.ninfer:ro \
  #     -p 8080:8080 oci.garvey.sh/ninfer:<tag>
  #
  # --max-context is 8192 by default, which is too small to hold pi's tool
  # schemas — the agent gets a 400 context_length_exceeded before it says
  # anything. 128k costs 8.46 GiB of KV cache and leaves ~5 GiB of the
  # 5090's VRAM free; ~192k is the wall for bf16 KV. Cold prefill runs at
  # ~2650 tok/s, so a full 128k prompt is ~50 s to first token.
  # The engine reads only v3 artifacts; upstream's
  # tools/upgrade_ninfer_v2_to_v3.py converts an older download.
  dockerImageFor = system:
    let
      pkgs = pkgsFor system;
      ninfer = ninferFor system;
      imageRoot = pkgs.buildEnv {
        name = "image-root";
        paths = [
          ninfer
          pkgs.cacert
          pkgs.busybox
        ];
        pathsToLink = [ "/bin" "/etc" "/share" ];
      };
    in
    # Pushed as oci.garvey.sh/ninfer:<tag>, the tag being the package version
    # plus the image root's store hash, so a rebuild with new contents gets a
    # new tag.
    pkgs.dockerTools.buildLayeredImage {
      name = "ninfer";
      tag = "${ninfer.version}-${builtins.substring 11 7 imageRoot.outPath}";

      contents = imageRoot;

      fakeRootCommands = ''
        mkdir -p /models /tmp
        chmod 1777 /tmp
      '';
      enableFakechroot = true;

      config = {
        Cmd = [
          "/bin/ninfer-serve"
          "/models/model.ninfer"
          "--host"
          "0.0.0.0"
          "--port"
          "8080"
          "--max-context"
          "131072"
        ];
        ExposedPorts = { "8080/tcp" = { }; };
        Volumes = { "/models" = { }; };
        Env = [
          "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        ];
      };
    };
in
{
  ninfer = ninferFor system;
  image = dockerImageFor system;
}
