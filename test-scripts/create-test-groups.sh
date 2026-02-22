#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Create test.group1 and test.group2 using gonewsd addgroup.
# Uses config from test.conf (SpoolDir etc).
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
cd "$ROOT"
CONF="${1:-$ROOT/test-scripts/test.conf}"

if [[ ! -f "$CONF" ]]; then
  echo "Usage: $0 [config-file]" >&2
  echo "  Default config: $ROOT/test-scripts/test.conf" >&2
  echo "  Config must set SpoolDir (e.g. ./test-scripts/spool)." >&2
  exit 1
fi
if [[ ! -x "$BINDIR/gonewsd" ]]; then
  echo "Build first: task build  (or: go build -o bin/gonewsd ./cmd/gonewsd)" >&2
  exit 1
fi

# Create a group with addgroup command (group perm: rw, world perm: r)
# Note: -c must come BEFORE the subcommand for Go's flag package to parse it
create_one() {
  local name="$1"
  local desc="$2"
  "$BINDIR/gonewsd" -c "$CONF" addgroup -group "$name" -g rw -o r \
    -desc "$desc" -creator "test-setup" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
}

echo "Creating test.group1 and test.group2 via gonewsd -c $CONF addgroup"
create_one "test.group1" "Test group 1 for testing"
create_one "test.group2" "Test group 2 for testing"

echo "Done. Groups test.group1 and test.group2 are ready."
echo "Start gonewsd with: $BINDIR/gonewsd -d -c $CONF"
echo "Then LIST should show both groups."
