# Points ngarvey's OrcaSlicer "Skyforge" printer preset at https://skyforge.home.garvey.sh with the API key from sops.
#
# skyforge's nginx lets a request through without an Authelia login only if its
# X-Api-Key matches secrets/skyforge-orca.yaml (hosts/skyforge/web-gate.nix),
# and an X-Api-Key header is the only credential OrcaSlicer can send to a
# Klipper printer.
#
# OrcaSlicer owns the preset file and rewrites it whenever the preset is saved,
# so it cannot be a read-only home-manager link. Instead each activation merges
# print_host and printhost_apikey into the existing file and leaves every other
# field alone. A preset that does not exist yet is not created: make it in
# OrcaSlicer (name "Skyforge") and the next deploy fills in the host and key.
# OrcaSlicer reads presets at startup, so restart it after a change; saving the
# preset from an instance that was already running writes the old values back.
#
# OrcaSlicer 2.4.2's Moonraker device agent does not support wss, so its live
# status stream in the Device tab stops working over https. Uploads and
# Send/Print use plain HTTPS requests and are not affected.
{ config, pkgs, ... }:

let
  keyFile = config.sops.secrets.skyforge-orca-key.path;
  host = "https://skyforge.home.garvey.sh";
in
{
  sops.secrets.skyforge-orca-key = {
    sopsFile = ../../secrets/skyforge-orca.yaml;
    key = "orca_api_key";
    owner = "ngarvey";
    mode = "0400";
  };

  home-manager.users.ngarvey = { lib, ... }: {
    # `|| true` and the guards keep a problem here from failing activation (and
    # tripping the deploy watchdog); it only logs.
    home.activation.orcaSkyforgeHost = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
      preset="$HOME/.config/OrcaSlicer/user/default/machine/Skyforge.json"
      if [ ! -f "$preset" ]; then
        echo "orca-skyforge: no Skyforge preset at $preset; create it in OrcaSlicer, then redeploy"
      elif [ ! -r ${keyFile} ]; then
        echo "orca-skyforge: ${keyFile} is not readable; leaving the preset alone"
      else
        tmp="$(mktemp "$preset.XXXXXX")"
        if ${pkgs.jq}/bin/jq --indent 4 --arg host ${host} --rawfile key ${keyFile} \
             '.print_host = $host | .printhost_apikey = ($key | rtrimstr("\n"))' \
             "$preset" > "$tmp" && ! ${pkgs.diffutils}/bin/cmp -s "$tmp" "$preset"; then
          run chmod --reference="$preset" "$tmp" && run mv "$tmp" "$preset" \
            && echo "orca-skyforge: updated $preset; restart OrcaSlicer to pick it up" || true
        fi
        rm -f "$tmp"
      fi
    '';
  };
}
