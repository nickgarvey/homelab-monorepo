#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REGISTRY="zot.home.arpa:5000"

ensure_policy() {
    mkdir -p ~/.config/containers
    if [ ! -f ~/.config/containers/policy.json ]; then
        echo '{"default":[{"type":"insecureAcceptAnything"}]}' > ~/.config/containers/policy.json
        echo "Created ~/.config/containers/policy.json"
    fi
}

list_packages() {
    cd "$SCRIPT_DIR"
    nix flake show --json 2>/dev/null | jq -r '.packages["x86_64-linux"] | keys[]'
}

do_build() {
    local pkg="$1"
    cd "$SCRIPT_DIR"
    echo "==> Building ${pkg}..."
    nix build ".#${pkg}"
    echo "==> Built ${pkg} -> ./result"
}

do_deploy() {
    local pkg="$1"
    do_build "$pkg"
    ensure_policy
    cd "$SCRIPT_DIR"

    echo "==> Inspecting built image..."
    # Read name:tag from manifest.json inside the archive
    local repo_tag
    repo_tag=$(tar -xzf result manifest.json -O | jq -r '.[0].RepoTags[0]')

    local image_name image_tag
    image_name="${repo_tag%%:*}"
    image_tag="${repo_tag##*:}"

    # Strip existing registry host prefix if present (e.g. "zot.home.arpa/foo" -> "foo")
    local repo
    if echo "$image_name" | grep -qE '^[^/]+\.[^/]+/'; then
        repo="${image_name#*/}"
    else
        repo="$image_name"
    fi

    local dest="docker://${REGISTRY}/${repo}:${image_tag}"
    echo "==> Pushing to ${dest}..."
    skopeo copy --dest-tls-verify=false "docker-archive:result" "$dest"
    echo "==> Done: ${pkg} deployed to ${dest}"
}

usage() {
    echo "Usage: $(basename "$0") <command> [package]"
    echo ""
    echo "Commands:"
    echo "  build  <package|all>  Build image(s) with nix"
    echo "  deploy <package|all>  Build and push image(s) to ${REGISTRY}"
    echo "  list                  List available packages"
    echo ""
    echo "Packages:"
    list_packages | sed 's/^/  /'
}

CMD="${1:-}"
PKG="${2:-}"

case "$CMD" in
    list)
        list_packages
        ;;
    build)
        [ -z "$PKG" ] && { usage; exit 1; }
        if [ "$PKG" = "all" ]; then
            for p in $(list_packages); do
                do_build "$p"
            done
        else
            do_build "$PKG"
        fi
        ;;
    deploy)
        [ -z "$PKG" ] && { usage; exit 1; }
        if [ "$PKG" = "all" ]; then
            for p in $(list_packages); do
                do_deploy "$p"
            done
        else
            do_deploy "$PKG"
        fi
        ;;
    *)
        usage
        exit 1
        ;;
esac
