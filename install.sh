#!/bin/bash
# Install aflock-tui — downloads the latest release binary for your platform.
#
# Usage:
#   bash <(curl -s https://raw.githubusercontent.com/manzil-infinity180/aflock-tui/main/install.sh)
#   bash <(curl -s https://raw.githubusercontent.com/manzil-infinity180/aflock-tui/main/install.sh) /custom/path
#
set -eou pipefail

REPO="manzil-infinity180/aflock-tui"
BINARY="aflock-tui"

TEMPDIR=$(mktemp -d)
trap 'rm -rf $TEMPDIR' EXIT

INSTALL_DIR=${1:-"/usr/local/bin"}

if [ -L "$INSTALL_DIR" ]; then
  INSTALL_DIR=$(readlink -f "$INSTALL_DIR" 2>/dev/null || readlink "$INSTALL_DIR")
fi

if [ ! -d "$INSTALL_DIR" ]; then
  echo "Install directory $INSTALL_DIR does not exist"
  exit 1
fi

# Get latest version
VERSION=$(curl -L -s "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": *"[^"]*"' | sed 's/"//g' | sed 's/tag_name: *//')

if [ -z "$VERSION" ]; then
  echo "Failed to get latest version from GitHub"
  exit 1
fi

# Strip 'v' prefix for filename
VERSION_NUM=${VERSION#v}

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
  echo "Unsupported OS: $OS (only linux and darwin are supported)"
  exit 1
fi

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

FILENAME="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/$VERSION/${BINARY}_${VERSION_NUM}_checksums.txt"

echo "aflock-tui $VERSION"
echo "Downloading for $OS/$ARCH..."
echo ""

# Download binary + checksums
cd "$TEMPDIR"
curl -sL -o "$FILENAME" "$DOWNLOAD_URL"
curl -sL -o checksums.txt "$CHECKSUMS_URL"

# Verify checksum
EXPECTED=$(grep -w "$FILENAME" checksums.txt | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
  echo "Warning: checksum not found for $FILENAME, skipping verification"
else
  if command -v sha256sum &>/dev/null; then
    ACTUAL=$(sha256sum "$FILENAME" | awk '{print $1}')
  else
    ACTUAL=$(shasum -a 256 "$FILENAME" | awk '{print $1}')
  fi

  if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Checksum verification FAILED"
    echo "  expected: $EXPECTED"
    echo "  got:      $ACTUAL"
    exit 1
  fi
  echo "Checksum verified"
fi

# Extract
tar -xzf "$FILENAME"

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY" "$INSTALL_DIR/"
else
  if [ -t 0 ]; then
    echo ""
    echo "Need sudo to install to $INSTALL_DIR"
    sudo mv "$BINARY" "$INSTALL_DIR/"
  else
    echo "No write permission for $INSTALL_DIR — run with sudo or specify a different directory:"
    echo "  bash install.sh ~/.local/bin"
    exit 1
  fi
fi

cd - >/dev/null

echo ""
echo "Installed $BINARY $VERSION_NUM to $INSTALL_DIR/$BINARY"
echo ""
echo "Usage:"
echo "  aflock-tui                                    # browse sessions"
echo "  aflock-tui replay session.jsonl policy.aflock  # replay"
echo "  aflock-tui watch --live policy.aflock          # live watch"
