#!/usr/bin/env bash
set -euo pipefail

REPO="datum-cloud/datum-mcp"

# Pretty output (disable with NO_COLOR or non-TTY)
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  C_RESET='\033[0m'
  C_BOLD='\033[1m'
  C_DIM='\033[2m'
  C_GREEN='\033[32m'
  C_CYAN='\033[36m'
  C_YELLOW='\033[33m'
  C_RED='\033[31m'
  ICON_OK="✅"
  ICON_INFO="ℹ️ "
  ICON_WARN="⚠️ "
  ICON_ERR="❌"
else
  C_RESET='' ; C_BOLD='' ; C_DIM='' ; C_GREEN='' ; C_CYAN='' ; C_YELLOW='' ; C_RED=''
  ICON_OK='[OK]'
  ICON_INFO='[i]'
  ICON_WARN='[!]'
  ICON_ERR='[x]'
fi

usage() {
  cat <<EOF
Install datum-mcp binary

Usage:
  $0                # install latest
  $0 v0.6.0         # install specific tag

The binary will be placed in the first writable of:
  - /usr/local/bin
  - ~/.local/bin
  - ~/bin
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

detect_platform() {
  local os arch uname_s uname_m
  uname_s="$(uname -s 2>/dev/null || echo unknown)"
  uname_m="$(uname -m 2>/dev/null || echo unknown)"

  case "$uname_s" in
    MINGW*|MSYS*|CYGWIN*|Windows_NT) os=windows ;;
    Darwin) os=darwin ;;
    Linux)  os=linux  ;;
    *) echo "Unsupported OS $uname_s" >&2; exit 1 ;;
  esac

  case "$uname_m" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "Unsupported arch $uname_m" >&2; exit 1 ;;
  esac
  echo "${os}_${arch}"
}

fetch_latest_tag() {
  # Resolve latest tag from public GitHub API without auth
  local api="https://api.github.com/repos/${REPO}/releases/latest"
  local tag
  tag=$(curl -fsSL "$api" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n1) || true

  if [[ -z "$tag" ]]; then
    # Fallback: follow redirects from /releases/latest and parse final URL (/tag/<ver>)
    local final
    final=$(curl -fsSL -o /dev/null -w '%{url_effective}' -L "https://github.com/${REPO}/releases/latest" 2>/dev/null) || true
    tag=$(printf '%s' "$final" | sed -n 's#.*/tag/\([^/]*\)$#\1#p')
  fi
  printf '%s' "$tag"
}

VERSION="${1:-latest}"
if [[ "$VERSION" == "latest" ]]; then
  printf "%b\n" "${C_CYAN}${ICON_INFO} Resolving latest release tag...${C_RESET}"
  VERSION="$(fetch_latest_tag)"
  if [[ -z "$VERSION" ]]; then
    printf "%b\n" "${C_RED}${ICON_ERR} Failed to resolve latest release tag${C_RESET}" >&2
    exit 1
  fi
fi

PLATFORM="$(detect_platform)" # e.g., darwin_arm64, windows_amd64
ASSET="datum-mcp_${PLATFORM}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

printf "%b\n" "${C_CYAN}${ICON_INFO} Downloading ${ASSET} ${C_DIM}(${VERSION})${C_RESET}"
TMP="$(mktemp)"
curl -fL -o "$TMP" "$URL"
chmod +x "$TMP" || true

case "$PLATFORM" in
  windows_*)
    # On Windows environments (Git Bash, MSYS), prefer ~/bin and .exe suffix
    dest_dir=""
    for d in "$HOME/bin" "$PWD"; do
      if mkdir -p "$d" 2>/dev/null && [[ -w "$d" ]]; then dest_dir="$d"; break; fi
    done
    if [[ -z "$dest_dir" ]]; then dest_dir="$PWD"; fi
    mv "$TMP" "$dest_dir/datum-mcp.exe"
    printf "%b\n" "${C_GREEN}${ICON_OK} Installed:${C_RESET} $dest_dir/datum-mcp.exe"
    ;;
  *)
    # Always prefer /usr/local/bin for system-wide install; elevate if needed
    dest_dir=""
    target_system_dir="/usr/local/bin"

    installed_system=""
    # Try without sudo if writable
    if mkdir -p "$target_system_dir" 2>/dev/null && [[ -w "$target_system_dir" ]]; then
      mv "$TMP" "$target_system_dir/datum-mcp"
      dest_dir="$target_system_dir"
      installed_system=1
    else
      # Try with sudo, prompting for password if needed
      if command -v sudo >/dev/null 2>&1; then
        printf "%b\n" "${C_CYAN}${ICON_INFO} Elevation required to install to ${target_system_dir}. You may be prompted for your password.${C_RESET}"
        if ( sudo mkdir -p "$target_system_dir" && sudo mv "$TMP" "$target_system_dir/datum-mcp" && sudo chmod +x "$target_system_dir/datum-mcp" ); then
          dest_dir="$target_system_dir"
          installed_system=1
        fi
      fi
    fi

    if [[ -n "$installed_system" ]]; then
      printf "%b\n" "${C_GREEN}${ICON_OK} Installed:${C_RESET} $dest_dir/datum-mcp"
    else
      # Fall back to user-writable locations (no sudo)
      for d in "$HOME/.local/bin" "$HOME/bin"; do
        if mkdir -p "$d" 2>/dev/null && [[ -w "$d" ]]; then dest_dir="$d"; break; fi
      done
      if [[ -z "$dest_dir" ]]; then dest_dir="$PWD"; fi
      mv "$TMP" "$dest_dir/datum-mcp"
      printf "%b\n" "${C_GREEN}${ICON_OK} Installed:${C_RESET} $dest_dir/datum-mcp"
      if [[ "$dest_dir" != "$target_system_dir" ]]; then
        printf "%b\n" "${C_YELLOW}${ICON_WARN} Could not install to ${target_system_dir}.${C_RESET}"
        if command -v sudo >/dev/null 2>&1; then
          printf "%b\n" "  To move it system-wide later, run:"
          printf "%b\n" "    ${C_DIM}sudo mv '$dest_dir/datum-mcp' '$target_system_dir/datum-mcp'${C_RESET}"
        else
          printf "%b\n" "  'sudo' not found. Move manually with sufficient privileges to a PATH dir, e.g.:"
          printf "%b\n" "    ${C_DIM}mv '$dest_dir/datum-mcp' '/usr/local/bin/datum-mcp'${C_RESET}"
        fi
      fi
    fi
    ;;
esac

# Guidance for final steps
echo
printf "%b\n" "${C_BOLD}Next steps:${C_RESET}"
case ":$PATH:" in
  *":$dest_dir:"*) printf "%b\n" "  - ${C_GREEN}'$dest_dir' is on your PATH. You're all set.${C_RESET}" ;;
  *)
    printf "%b\n" "  - ${C_YELLOW}Add '$dest_dir' to your PATH, for example:${C_RESET}"
    printf "%b\n" "      ${C_DIM}echo 'export PATH=\"$dest_dir:\$PATH\"' >> ~/.bashrc  # or ~/.zshrc${C_RESET}"
    printf "%b\n" "    or move it to a standard location (may require sudo):"
    printf "%b\n" "      ${C_DIM}sudo mv '$dest_dir/datum-mcp' /usr/local/bin/datum-mcp${C_RESET}"
    ;;
esac

echo
printf "%b\n" "${C_BOLD}To run in stdio (default):${C_RESET}"
case "$PLATFORM" in
  windows_*)
    printf "%b\n" "  ${C_CYAN}datum-mcp.exe${C_RESET}"
  printf "%b\n" "${C_BOLD}To run as HTTP server on port 9000:${C_RESET}"
  printf "%b\n" "  ${C_CYAN}datum-mcp.exe --mode http --host localhost --port 9000${C_RESET}"
    ;;
  *)
    printf "%b\n" "  ${C_CYAN}datum-mcp${C_RESET}"
    printf "%b\n" "${C_BOLD}To run as HTTP server on port 9000:${C_RESET}"
    printf "%b\n" "  ${C_CYAN}datum-mcp --mode http --host localhost --port 9000${C_RESET}"
    ;;
esac

