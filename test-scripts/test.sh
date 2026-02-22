#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
"$BINDIR/gonewsd" -d -c "$SCRIPT_DIR/test.conf"
