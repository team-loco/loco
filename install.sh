#!/bin/sh
set -eu

repo="team-loco/loco"
version="${LOCO_VERSION:-latest}"
install_dir="${LOCO_INSTALL_DIR:-${HOME:-}/.local/bin}"

usage() {
  printf 'Usage: install.sh [--version VERSION] [--bin-dir DIRECTORY]\n'
  printf '\n'
  printf 'Install the Loco CLI from a GitHub release.\n'
  printf '\n'
  printf 'Options:\n'
  printf '  -v, --version VERSION  Install a release tag (default: latest)\n'
  printf '  -b, --bin-dir DIR      Install directory (default: ~/.local/bin)\n'
  printf '  -h, --help             Show this help\n'
}

error() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    -v|--version)
      [ "$#" -ge 2 ] || error "--version requires a value"
      version="$2"
      shift 2
      ;;
    --version=*)
      version="${1#*=}"
      shift
      ;;
    -b|--bin-dir)
      [ "$#" -ge 2 ] || error "--bin-dir requires a value"
      install_dir="$2"
      shift 2
      ;;
    --bin-dir=*)
      install_dir="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "unknown option: $1"
      ;;
  esac
done

[ -n "$install_dir" ] || error 'HOME is not set; specify --bin-dir'
[ -n "$version" ] || error 'version cannot be empty'

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) error "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) error "unsupported architecture: $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  download() {
    curl --fail --silent --show-error --location "$1" --output "$2"
  }
elif command -v wget >/dev/null 2>&1; then
  download() {
    wget --quiet --output-document="$2" "$1"
  }
else
  error 'curl or wget is required to download loco'
fi

if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  error 'sha256sum or shasum is required to verify the download'
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/loco-install.XXXXXX")" || error 'could not create a temporary directory'
trap 'rm -rf "$tmp_dir"' EXIT
trap 'exit 1' HUP INT TERM
asset="loco-${os}-${arch}"
if [ "$version" = latest ]; then
  release_url="https://github.com/${repo}/releases/latest/download"
else
  release_url="https://github.com/${repo}/releases/download/${version}"
fi

printf 'Downloading Loco CLI (%s, %s)\n' "$os" "$arch"
download "${release_url}/${asset}" "${tmp_dir}/${asset}" || error "download failed for ${asset} (${version})"
download "${release_url}/checksums.txt" "${tmp_dir}/checksums.txt" || error "could not download release checksums (${version})"

expected="$(awk -v asset="$asset" '$2 == asset {print $1}' "${tmp_dir}/checksums.txt")"
actual="$(checksum "${tmp_dir}/${asset}")"
[ -n "$expected" ] && [ "$actual" = "$expected" ] || error "checksum verification failed for ${asset}"

mkdir -p "$install_dir" || error "could not create install directory: ${install_dir}"
chmod 0755 "$tmp_dir/$asset"
mv "$tmp_dir/$asset" "$install_dir/loco" || error "could not install loco to ${install_dir}"
printf 'Installed loco to %s/loco\n' "$install_dir"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *)
    printf 'Add this directory to your PATH to run loco from any shell:\n'
    printf '  export PATH="%s:$PATH"\n' "$install_dir"
    ;;
esac
