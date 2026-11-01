# ThinkRail (packages/thinkrail), JetBrains' agentic IDE around pi, with the
# same hosted-LLM API keys as the pi module, plus an Anthropic key of its own.
#
# ThinkRail embeds its own pi engine and reads ~/.pi/agent, so it shares
# models.json and auth.json with the pi CLI from modules/llms/pi.nix. The API
# keys are not there: pi.nix puts them only in the `pi` wrapper's environment,
# so this wrapper reads the same sops secrets at launch. The Anthropic key is
# declared here because only ThinkRail uses it.

{ config, lib, pkgs, ... }:

let
  cfg = config.homelab.thinkrail;

  secrets = config.sops.secrets;

  thinkrail = pkgs.symlinkJoin {
    name = "thinkrail-with-keys";
    paths = [ (pkgs.callPackage ../../packages/thinkrail { }) ];
    nativeBuildInputs = [ pkgs.makeWrapper ];
    postBuild = ''
      wrapProgram $out/bin/thinkrail \
        --run 'export DEEPSEEK_API_KEY="$(< ${secrets.deepseek-api-key.path})"' \
        --run 'export FIREWORKS_API_KEY="$(< ${secrets.fireworks-api-key.path})"' \
        --run 'export OPENAI_API_KEY="$(< ${secrets.openai-api-key.path})"' \
        --run 'export ANTHROPIC_API_KEY="$(< ${secrets.anthropic-api-key.path})"'
    '';
  };
in
{
  options.homelab.thinkrail.enable = lib.mkEnableOption "ThinkRail for the ngarvey user";

  config = lib.mkIf cfg.enable {
    assertions = [{
      assertion = config.homelab.pi.enable;
      message = "homelab.thinkrail needs homelab.pi: it uses pi's API-key secrets and ~/.pi/agent.";
    }];

    sops.secrets.anthropic-api-key = {
      sopsFile = ../../secrets/llm-api-keys.yaml;
      owner = "ngarvey";
    };

    environment.systemPackages = [ thinkrail ];
  };
}
