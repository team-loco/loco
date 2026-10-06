#!/bin/sh
set -eu

repo="team-loco/loco"
install_dir="${LOCO_INSTALL_DIR:-${HOME}/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  printf 'curl is required to install loco\n' >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  printf 'sha256sum or shasum is required to install loco\n' >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM
asset="loco-${os}-${arch}"
release_url="https://github.com/${repo}/releases/latest/download"

curl -fsSL "${release_url}/${asset}" -o "${tmp_dir}/${asset}"
curl -fsSL "${release_url}/checksums.txt" -o "${tmp_dir}/checksums.txt"

expected="$(awk -v asset="$asset" '$2 == asset {print $1}' "${tmp_dir}/checksums.txt")"
if [ -z "$expected" ] || [ "$(checksum "${tmp_dir}/${asset}")" != "$expected" ]; then
  printf 'Checksum verification failed for %s\n' "$asset" >&2
  exit 1
fi

mkdir -p "$install_dir"
install -m 0755 "${tmp_dir}/${asset}" "${install_dir}/loco"
printf 'Installed loco to %s/loco\n' "$install_dir"
