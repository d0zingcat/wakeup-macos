#!/bin/bash
set -e

# Quick install script for wakeup-macos
# Usage: ./scripts/install.sh

echo "Building wakeup..."
make build

echo ""
echo "Starting interactive installation (requires sudo)..."
echo ""
sudo ./wakeup install
