# LAN inventory page — DHCP leases, reservations and neighbours as one table. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  # Only ever runs on the router, which is x86_64. Widen the list if that
  # changes; nothing here is architecture-specific.
  supportedSystems = [ "x86_64-linux" ];

  lanTableFor = system:
    let
      pkgs = nixpkgs.legacyPackages.${system};
    in
    pkgs.buildGoModule {
      pname = "lan-table";
      version = "0.1.0";
      src = ./.;
      # Standard library only.
      vendorHash = null;

      nativeBuildInputs = [ pkgs.makeWrapper ];

      # The two data sources are shelled out to by name. `--suffix` rather
      # than `--prefix` on purpose: on the router the system's own
      # networkctl must win, because it talks to the running networkd over
      # varlink and a version skew there is a real failure mode. The
      # suffix only makes the binary work when run outside NixOS.
      # buildGoModule names the binary after its directory (cmd/server).
      postInstall = ''
        mv $out/bin/server $out/bin/lan-table
        wrapProgram $out/bin/lan-table \
          --suffix PATH : ${nixpkgs.lib.makeBinPath [ pkgs.iproute2 pkgs.systemd ]}
      '';

      meta = {
        description = "Web page listing every device on the LAN";
        mainProgram = "lan-table";
        license = nixpkgs.lib.licenses.mit;
        platforms = supportedSystems;
      };
    };
in
lanTableFor system
