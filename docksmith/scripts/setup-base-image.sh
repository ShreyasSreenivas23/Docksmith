#!/bin/bash
# setup-base-image.sh
# One-time setup script to download and import the Alpine base image.
#
# This script uses Docker to pull alpine:3.18, export its filesystem as a tarball,
# then uses docksmith import to register it as a base image.
#
# Prerequisites:
#   - Docker must be installed and running
#   - docksmith binary must be built (go build -o docksmith .)
#
# Usage:
#   chmod +x scripts/setup-base-image.sh
#   sudo ./scripts/setup-base-image.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
DOCKSMITH="${PROJECT_DIR}/docksmith"

echo "=== Docksmith Base Image Setup ==="
echo ""

# Check if docksmith binary exists.
if [ ! -f "$DOCKSMITH" ]; then
    echo "Building docksmith binary..."
    cd "$PROJECT_DIR"
    go build -o docksmith .
    echo "Built: $DOCKSMITH"
fi

# Check if Docker is available.
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is not installed or not in PATH."
    echo "Docker is needed only for the initial base image setup."
    exit 1
fi

# Create a temporary directory for the rootfs export.
TMPDIR=$(mktemp -d)
ROOTFS_TAR="${TMPDIR}/alpine-rootfs.tar"

echo "Pulling alpine:3.18..."
docker pull alpine:3.18

echo "Exporting Alpine rootfs..."
CONTAINER_ID=$(docker create alpine:3.18)
docker export "$CONTAINER_ID" > "$ROOTFS_TAR"
docker rm "$CONTAINER_ID" > /dev/null

echo "Importing into Docksmith..."
"$DOCKSMITH" import -t alpine:3.18 "$ROOTFS_TAR"

# Clean up.
rm -rf "$TMPDIR"

echo ""
echo "=== Setup Complete ==="
echo "Base image alpine:3.18 is now available."
echo ""
echo "You can now build and run images:"
echo "  sudo ./docksmith build -t myapp:latest ./sample-app"
echo "  sudo ./docksmith run myapp:latest"
echo ""
echo "To list images:"
echo "  sudo ./docksmith images"
