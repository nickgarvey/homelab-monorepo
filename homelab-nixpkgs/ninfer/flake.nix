{
  description = "NInfer inference engine (Qwen3.8-27B, RTX 5090 / sm_120a)";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs, ... }:
    let
      # Upstream compiles for sm_120a and nothing else, so there is no point
      # pretending this builds anywhere but an x86_64 host with an RTX 5090.
      supportedSystems = [ "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

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
          version = "0-unstable-2026-08-28";

          src = pkgs.fetchFromGitHub {
            owner = "Neroued";
            repo = "ninfer";
            rev = "3a61ef3f8ad58a6ad22d75912e4d0156f59caf2f";
            hash = "sha256-xpNyxYM8QMuylU9sv4LUdD8/iVxbDiArughk/vvME2s=";
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
            pkgs.ffmpeg
            pkgs.curl
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
          installPhase = ''
            runHook preInstall
            install -Dm755 apps/ninfer       $out/bin/ninfer
            install -Dm755 apps/ninfer-serve $out/bin/ninfer-serve
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
      #     -p 8080:8080 ninfer:latest
      dockerImageFor = system:
        let
          pkgs = pkgsFor system;
        in
        pkgs.dockerTools.buildLayeredImage {
          name = "ninfer";
          tag = "latest";

          contents = pkgs.buildEnv {
            name = "image-root";
            paths = [
              (ninferFor system)
              pkgs.cacert
              pkgs.busybox
            ];
            pathsToLink = [ "/bin" "/etc" "/share" ];
          };

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
      packages = forAllSystems (system: rec {
        ninfer = ninferFor system;
        default = ninfer;
        docker = dockerImageFor system;
      });

      dockerImage = forAllSystems dockerImageFor;

      devShells = forAllSystems (system:
        let
          pkgs = pkgsFor system;
          cuda = pkgs.cudaPackages_13_1;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.cmake
              pkgs.ninja
              pkgs.pkg-config
              pkgs.ffmpeg
              pkgs.curl
              cuda.cuda_nvcc
              cuda.cuda_cudart
            ];
          };
        });
    };
}
