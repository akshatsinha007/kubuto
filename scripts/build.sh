#!/bin/bash
set -e

echo "Building kubuto..."
mkdir -p bin
go build -v -o ./bin/kubuto ./cmd/kubuto
echo "Build successful! Binary is at ./bin/kubuto"
