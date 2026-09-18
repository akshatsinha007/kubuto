#!/bin/bash
set -e

echo "Running tests..."
go test -v ./...
echo "All tests passed."
