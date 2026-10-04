#!/bin/sh
# Install mcpit (Linux, macOS). Usage:
#   curl -fsSL https://github.com/jstdlee/mcpit/releases/latest/download/install.sh | sh
# Options (environment): MCPIT_VERSION=v0.3.12  MCPIT_BIN_DIR=$HOME/.local/bin
set -eu
repo="jstdlee/mcpit"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) echo "mcpit: unsupported OS $os (use install.ps1 on Windows)" >&2; exit 1 ;; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "mcpit: unsupported CPU $arch" >&2; exit 1 ;; esac
name="mcpit_${os}_${arch}"
if [ -n "${MCPIT_VERSION:-}" ]; then base="https://github.com/$repo/releases/download/$MCPIT_VERSION"; else base="https://github.com/$repo/releases/latest/download"; fi
bin="${MCPIT_BIN_DIR:-$HOME/.local/bin}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $base/$name.tar.gz"
curl -fsSL "$base/$name.tar.gz" -o "$tmp/$name.tar.gz"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
want=$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1); else got=$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1); fi
[ "$want" = "$got" ] || { echo "mcpit: checksum mismatch" >&2; exit 1; }
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$bin"
install -m 0755 "$tmp/$name/mcpit" "$bin/mcpit"
echo "installed $("$bin/mcpit" version) to $bin/mcpit"
case ":$PATH:" in *":$bin:"*) ;; *) echo "add $bin to your PATH, for example: export PATH=\"$bin:\$PATH\"" ;; esac
echo "next: mcpit doctor   then   mcpit setup <claude|codex|cursor|gemini|omp|vscode>"
