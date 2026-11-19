#!/bin/sh
# PTZ controller installer for macOS and Linux.
#
#   curl -fsSL https://github.com/__REPO__/releases/latest/download/install.sh | sh
#
# Downloads the latest release for this computer, checks its SHA-256
# against the release's SHA256SUMS, installs it as a service that starts
# with the computer, and opens the setup wizard. Run it again to update.
set -eu

REPO="${PTZ_REPO:-__REPO__}"
BASE="https://github.com/$REPO/releases/latest/download"

case "$(uname -s)" in
  Darwin) os=darwin; dir=/usr/local/ptz ;;
  Linux)  os=linux;  dir=/opt/ptz ;;
  *) echo "Unsupported system: $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported processor: $(uname -m)" >&2; exit 1 ;;
esac
asset="ptz-$os-$arch"

sudo=""
[ "$(id -u)" -eq 0 ] || sudo="sudo"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $asset from $REPO …"
curl -fsSL -o "$tmp/$asset" "$BASE/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$BASE/SHA256SUMS"

want="$(grep " $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  got="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "Checksum mismatch for $asset — not installing." >&2
  exit 1
fi
echo "Checksum OK."

if [ -x "$dir/ptz" ]; then
  echo "Updating the existing install in $dir"
  $sudo "$dir/ptz" service stop >/dev/null 2>&1 || true
fi
$sudo mkdir -p "$dir"
$sudo install -m 0755 "$tmp/$asset" "$dir/ptz"
[ "$os" = darwin ] && $sudo xattr -d com.apple.quarantine "$dir/ptz" 2>/dev/null || true

$sudo "$dir/ptz" service install >/dev/null 2>&1 || true   # already installed is fine
$sudo "$dir/ptz" service start

url="http://localhost:8080/setup"
echo
echo "PTZ controller $("$dir/ptz" version) is running."
echo "Finish setup at: $url"
if [ "$os" = darwin ]; then open "$url" 2>/dev/null || true
elif [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then xdg-open "$url" >/dev/null 2>&1 || true
fi
