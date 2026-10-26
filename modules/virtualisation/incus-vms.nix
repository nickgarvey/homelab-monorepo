# Incus VMs this host owns (homelab.incus.vms): each VM's project, profile and restricted client certificate, and its first creation from the seed image.

{ config, lib, pkgs, inputs, ... }:

let
  cfg = config.homelab.incus;
  incus = "${config.virtualisation.incus.package.client}/bin/incus";
  inherit (import ../networking/lan-hosts.nix) lanHosts;

  # Every VM starts as the seed (hosts/vms/seed.nix); the deploy tool installs
  # its own configuration after that. Seeding from the VM's own configuration
  # would change this host's closure on every change to the VM.
  seed = inputs.self.nixosConfigurations.incus-vm-seed.config.system.build;
  seedAlias = "incus-vm-seed-" + builtins.substring 11 8 (toString seed.repartImage);

  vmType = lib.types.submodule ({ name, ... }: {
    options = {
      project = lib.mkOption {
        type = lib.types.str;
        default = name;
        description = "Incus project the VM lives in. It gets its own profiles, images and volumes.";
      };
      cpus = lib.mkOption {
        type = lib.types.ints.positive;
        description = "limits.cpu";
      };
      memory = lib.mkOption {
        type = lib.types.str;
        example = "8GiB";
        description = "limits.memory";
      };
      diskSize = lib.mkOption {
        type = lib.types.str;
        example = "40GiB";
        description = "Root disk size on the default pool.";
      };
      bridge = lib.mkOption {
        type = lib.types.str;
        default = "vmbr0";
        description = "Host bridge the VM's NIC joins.";
      };
      mac = lib.mkOption {
        type = lib.types.str;
        default = (lib.findFirst (h: h.hostname == name)
          (throw "incus-vms: ${name} has no entry in modules/networking/lan-hosts.nix") lanHosts).mac;
        description = "NIC MAC address. The router gives the VM its static lease and DNS names by it.";
      };
      clientCertificates = lib.mkOption {
        type = lib.types.attrsOf lib.types.path;
        default = { };
        example = lib.literalExpression "{ mcp = ./incus-client.crt; }";
        description = ''
          PEM client certificates to trust, restricted to the VM's project,
          keyed by client (trusted as <vm>-<client>). Their keys stay on the
          clients (see docs/browser-vm.md).
        '';
      };
    };
  });

  # Creates what is missing and never deletes or recreates anything: removing
  # a VM from the config leaves it running until someone deletes it by hand.
  ensureVm = name: vm: ''
    ${lib.concatStrings (lib.mapAttrsToList (client: cert: ''
      fp=$(${pkgs.openssl}/bin/openssl x509 -in ${cert} -noout -fingerprint -sha256 \
        | cut -d= -f2 | tr -d : | tr A-F a-f)
      if ! ${incus} config trust show "$fp" >/dev/null 2>&1; then
        ${incus} config trust add-certificate ${cert} \
          --name ${name}-${client} --restricted --projects ${vm.project}
      fi
    '') vm.clientCertificates)}
    if ! ${incus} info ${name} --project ${vm.project} >/dev/null 2>&1; then
      if ! ${incus} image info ${seedAlias} --project ${vm.project} >/dev/null 2>&1; then
        ${incus} image import --project ${vm.project} --alias ${seedAlias} \
          ${seed.metadata}/tarball/*.tar.xz ${seed.repartImage}/*.qcow2
      fi
      ${incus} init ${seedAlias} ${name} --vm --project ${vm.project} --profile ${name}
      ${incus} start ${name} --project ${vm.project}
    fi
  '';
in
{
  options.homelab.incus.vms = lib.mkOption {
    type = lib.types.attrsOf vmType;
    default = { };
    description = ''
      Incus VMs owned by this host, keyed by instance name. Each needs a NixOS
      configuration of the same name in hosts/vms/ and a deploy tool entry
      with this host as its Parent.
    '';
  };

  config = lib.mkIf (cfg.enable && cfg.vms != { }) {
    virtualisation.incus.preseed = {
      projects = lib.mapAttrsToList (name: vm: {
        name = vm.project;
        description = "Incus VM ${name} (modules/virtualisation/incus-vms.nix)";
        config = {
          "features.images" = "true";
          "features.profiles" = "true";
          "features.storage.volumes" = "true";
          "features.storage.buckets" = "true";
          "features.networks" = "false";
        };
      }) cfg.vms;

      # Preseed merges into existing profiles and never removes keys: to drop
      # a setting, unset it by hand as well.
      profiles = lib.mapAttrsToList (name: vm: {
        inherit name;
        project = vm.project;
        description = "Incus VM ${name} (modules/virtualisation/incus-vms.nix)";
        config = {
          "limits.cpu" = toString vm.cpus;
          "limits.memory" = vm.memory;
          "boot.autostart" = "true";
          # NixOS boots an unsigned UKI.
          "security.secureboot" = "false";
        };
        devices = {
          root = {
            type = "disk";
            path = "/";
            pool = "default";
            size = vm.diskSize;
          };
          eth0 = {
            type = "nic";
            nictype = "bridged";
            parent = vm.bridge;
            hwaddr = vm.mac;
          };
        };
      }) cfg.vms;
    };

    systemd.services.incus-vms = {
      description = "Create missing Incus VMs from the seed image";
      after = [ "incus.service" "incus-preseed.service" ];
      requires = [ "incus.service" "incus-preseed.service" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
      };
      script = lib.concatStrings (lib.mapAttrsToList ensureVm cfg.vms);
    };
  };
}
