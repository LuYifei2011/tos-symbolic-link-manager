#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p build
GOTOOLCHAIN=local go build -o build/dev-server ./server
exec ./build/dev-server --dev --web dist "$@"
