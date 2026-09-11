#!/usr/bin/env bash
set -euo pipefail

REPO="nxxxsooo/another"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-latest}"

configure_path() {
  default_dir="$HOME/.local/bin"

  # A custom destination is an explicit caller choice. Do not guess which
  # shell file should own an arbitrary path (or try to quote it as shell code).
  if [ "$INSTALL_DIR" != "$default_dir" ]; then
    case ":${PATH:-}:" in
      *":$INSTALL_DIR:"*) return ;;
    esac
    echo "Add $INSTALL_DIR to PATH before running another." >&2
    return
  fi

  # If the launching shell already exposes the directory, its startup files
  # are doing their job. Avoid adding a duplicate entry to a second file.
  case ":${PATH:-}:" in
    *":$default_dir:"*) return ;;
  esac

  # Keep HOME and PATH literal: this line is written for the next shell.
  # shellcheck disable=SC2016
  path_line='export PATH="$HOME/.local/bin:$PATH"'
  case "${SHELL:-}" in
    */zsh|zsh) path_file="${ZDOTDIR:-$HOME}/.zshrc" ;;
    */bash|bash) path_file="$HOME/.bashrc" ;;
    *) path_file="$HOME/.profile" ;;
  esac

  mkdir -p "$(dirname "$path_file")"
  if [ -f "$path_file" ] && grep -Fqx "$path_line" "$path_file"; then
    return
  fi

  # Start on a fresh line even when an existing rc file has no trailing LF.
  if [ -s "$path_file" ]; then
    printf '\n%s\n' "$path_line" >> "$path_file"
  else
    printf '%s\n' "$path_line" >> "$path_file"
  fi
  echo "Added $default_dir to PATH in $path_file"
  echo "For this shell, run: $path_line"
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)"
fi

url="https://github.com/${REPO}/releases/download/${VERSION}/another_${VERSION#v}_${os}_${arch}.tar.gz"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Installing another ${VERSION} for ${os}/${arch}..."
curl -fsSL "$url" | tar -xz -C "$tmpdir"
mkdir -p "$INSTALL_DIR"
install -m 755 "$tmpdir/another" "$INSTALL_DIR/another"
echo "Installed to $INSTALL_DIR/another"
configure_path
case ":${PATH:-}:" in
  *":$INSTALL_DIR:"*) echo "Run: another --help" ;;
  *) echo "Run now: $INSTALL_DIR/another --help" ;;
esac
