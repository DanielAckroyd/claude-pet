#!/bin/sh
# claude-pet installer for macOS and Linux (Windows: install.ps1).
#   curl -fsSL https://raw.githubusercontent.com/DanielAckroyd/claude-pet/main/install.sh | sh
# Downloads the latest release binary, checks its checksum, puts it in ~/.local/bin
# (override with CLAUDE_PET_BIN=/some/dir), then runs `claude-pet setup`.
set -eu

repo=DanielAckroyd/claude-pet
dir=${CLAUDE_PET_BIN:-$HOME/.local/bin}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in
  darwin | linux) ;;
  *) echo "claude-pet: unsupported OS '$os' (on Windows, use install.ps1)" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "claude-pet: unsupported CPU '$(uname -m)'" >&2; exit 1 ;;
esac

name="claude-pet_${os}_${arch}.tar.gz"
base=${CLAUDE_PET_BASE:-"https://github.com/$repo/releases/latest/download"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $name"
curl -fsSL "$base/$name" -o "$tmp/$name"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want=$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$name" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "claude-pet: checksum mismatch for $name, not installing" >&2
  exit 1
fi

tar -xzf "$tmp/$name" -C "$tmp" claude-pet
mkdir -p "$dir"
mv "$tmp/claude-pet" "$dir/claude-pet"
chmod +x "$dir/claude-pet"
echo "Installed $("$dir/claude-pet" version) to $dir"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Note: $dir isn't on your PATH yet. Add it to your shell profile to use 'claude-pet' directly." ;;
esac

# Piped through sh, stdin is this script, so hand setup the terminal when there is one.
if [ -t 1 ] && (exec </dev/tty) 2>/dev/null; then
  echo
  "$dir/claude-pet" setup </dev/tty
else
  echo "Now run: claude-pet setup"
fi
