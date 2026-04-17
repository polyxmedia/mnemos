#!/usr/bin/env bash
# Mnemos one-line installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
#
# Detects platform, downloads the latest release binary, installs it to
# ~/.local/bin (or /usr/local/bin as fallback), and auto-registers with
# Claude Code / Claude Desktop / Cursor / Windsurf / Codex via `mnemos init`.

set -euo pipefail

REPO="polyxmedia/mnemos"
BINARY="mnemos"

log() { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
err() { printf "\033[1;31m✗\033[0m %s\n" "$*" >&2; exit 1; }
ok()  { printf "\033[1;32m✓\033[0m %s\n" "$*"; }

detect_os() {
  case "$(uname -s)" in
    Linux*)   echo "Linux" ;;
    Darwin*)  echo "Darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "Windows" ;;
    *) err "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "x86_64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) err "unsupported architecture: $(uname -m)" ;;
  esac
}

pick_install_dir() {
  # Prefer a user-writable dir already on PATH.
  for candidate in "$HOME/.local/bin" "/usr/local/bin"; do
    if [[ -d "$candidate" && -w "$candidate" ]]; then
      echo "$candidate"; return
    fi
  done
  # Fall back: create ~/.local/bin.
  mkdir -p "$HOME/.local/bin"
  echo "$HOME/.local/bin"
}

latest_version() {
  # GitHub releases API returns the tag for the latest non-prerelease.
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1
}

fallback_from_source() {
  log "no released binaries yet — falling back to \`go install\`"
  if ! command -v go >/dev/null 2>&1; then
    err "go toolchain not found. install Go 1.23+ or wait for a released binary."
  fi
  local gobin="${GOPATH:-$HOME/go}/bin"
  if ! GOBIN="$gobin" go install github.com/polyxmedia/mnemos/cmd/mnemos@latest; then
    err "go install failed. run manually: go install github.com/polyxmedia/mnemos/cmd/mnemos@latest"
  fi
  ok "installed $gobin/mnemos"
  log "registering with agent clients"
  "$gobin/mnemos" init || true
  echo
  ok "done. restart your agent."
  exit 0
}

main() {
  local os arch version install_dir tmp archive asset url
  os="$(detect_os)"
  arch="$(detect_arch)"
  install_dir="$(pick_install_dir)"

  log "detecting latest mnemos release"
  version="$(latest_version || true)"
  if [[ -z "${version:-}" ]]; then
    # No released tags yet — fall back to source install so pre-release
    # users aren't stranded.
    fallback_from_source
  fi
  version="${version#v}"
  ok "version: v${version}"

  if [[ "$os" == "Windows" ]]; then
    asset="${BINARY}_${version}_${os}_${arch}.zip"
  else
    asset="${BINARY}_${version}_${os}_${arch}.tar.gz"
  fi
  url="https://github.com/${REPO}/releases/download/v${version}/${asset}"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  log "downloading $asset"
  archive="${tmp}/${asset}"
  if ! curl -fsSL -o "$archive" "$url"; then
    err "download failed: $url"
  fi

  log "extracting"
  case "$asset" in
    *.tar.gz) tar -xzf "$archive" -C "$tmp" ;;
    *.zip)    (cd "$tmp" && unzip -q "$archive") ;;
  esac

  log "installing to $install_dir"
  install -m 0755 "${tmp}/${BINARY}" "${install_dir}/${BINARY}"
  ok "installed ${install_dir}/${BINARY}"

  # PATH check.
  case ":$PATH:" in
    *":${install_dir}:"*) ;;
    *)
      log "adding $install_dir to shell rc"
      for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
        [[ -f "$rc" ]] || continue
        if ! grep -q "$install_dir" "$rc"; then
          printf '\nexport PATH="%s:$PATH"\n' "$install_dir" >> "$rc"
          ok "updated $rc"
        fi
      done
      ;;
  esac

  log "registering with agent clients (Claude Code / Claude Desktop / Cursor / Windsurf / Codex)"
  "${install_dir}/${BINARY}" init || true

  echo
  ok "done. restart your agent to pick up mnemos."
  echo "run: ${install_dir}/${BINARY} doctor"
}

main "$@"
