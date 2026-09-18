#!/usr/bin/env bash
# Installs kubuto: detects OS/arch, downloads the matching release
# binary, and installs it. Usage:
#   curl -sSL https://raw.githubusercontent.com/akshatsinha007/kubuto/main/install.sh | sh
#
# Override the install location with KUBUTO_INSTALL_DIR (default
# /usr/local/bin), or pin a specific release with KUBUTO_VERSION
# (e.g. KUBUTO_VERSION=v0.1.0) instead of installing the latest one.
set -euo pipefail

REPO="akshatsinha007/kubuto"
INSTALL_DIR="${KUBUTO_INSTALL_DIR:-/usr/local/bin}"

detect_os() {
	case "$(uname -s)" in
	Darwin) echo "darwin" ;;
	Linux) echo "linux" ;;
	*) echo "" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo "amd64" ;;
	arm64 | aarch64) echo "arm64" ;;
	*) echo "" ;;
	esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ -z "$OS" ] || [ -z "$ARCH" ]; then
	echo "kubuto: unsupported platform ($(uname -s)/$(uname -m))." >&2
	echo "Published binaries only cover darwin/linux on amd64/arm64 — see https://github.com/$REPO/releases" >&2
	exit 1
fi

for bin in curl tar; do
	command -v "$bin" >/dev/null 2>&1 || {
		echo "kubuto: '$bin' is required but not found" >&2
		exit 1
	}
done

# The release filenames embed the version (e.g. kubuto_0.1.0_darwin_arm64.tar.gz),
# so GitHub's /releases/latest/download/<file> alias can't be used directly —
# it needs the exact filename, which needs the version resolved first.
TAG="${KUBUTO_VERSION:-}"
if [ -z "$TAG" ]; then
	TAG="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
fi
if [ -z "$TAG" ]; then
	echo "kubuto: couldn't determine the latest release version — set KUBUTO_VERSION to pin one" >&2
	exit 1
fi

VERSION_NO_V="${TAG#v}"
ARCHIVE="kubuto_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/${TAG}/${ARCHIVE}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "kubuto: downloading ${TAG} for ${OS}/${ARCH}..."
curl -fsSL -o "$TMP_DIR/$ARCHIVE" "$URL"
tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR" kubuto

mkdir -p "$INSTALL_DIR"
if [ -w "$INSTALL_DIR" ]; then
	mv "$TMP_DIR/kubuto" "$INSTALL_DIR/kubuto"
else
	echo "kubuto: elevated permission needed to write to $INSTALL_DIR"
	sudo mv "$TMP_DIR/kubuto" "$INSTALL_DIR/kubuto"
fi
chmod +x "$INSTALL_DIR/kubuto"

echo "kubuto: installed to $INSTALL_DIR/kubuto"
"$INSTALL_DIR/kubuto" --version
