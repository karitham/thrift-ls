#!/usr/bin/env bash
set -euo pipefail

BIN_DIR=$(mktemp -d)
trap 'rm -rf "$BIN_DIR"' EXIT
go build -o "$BIN_DIR/thriftls" .
export THRIFTLS_BIN="$BIN_DIR/thriftls"

for f in ./tests/e2e/*
do
	if test -d "$f"; then
		bash "$f"/test.sh
	fi
done
