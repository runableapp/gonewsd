#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Test mail gateway: cat email_message | gonewsd mailgateway <group>
# Same usage as newsd: cat email_message | newsd -mailgateway rush.general
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
cd "$ROOT"

CONF="${CONF:-$ROOT/test-scripts/test.conf}"
# Default spool comes from config (SpoolDir). Can be overridden via env SPOOL.
DEFAULT_SPOOL="$(awk 'tolower($1)=="spooldir"{print $2; exit}' "$CONF" 2>/dev/null || true)"
SPOOL="${SPOOL:-${DEFAULT_SPOOL:-$ROOT/test-scripts/spool}}"
GROUP="${1:-test.group1}"

# Ensure binary exists
if [ ! -x "$BINDIR/gonewsd" ]; then
  echo "Building gonewsd (task build)..."
  task build 2>/dev/null || { mkdir -p bin; go build -o bin/gonewsd ./cmd/gonewsd; }
fi

# Ensure test groups exist (same layout as create-test-groups.sh)
if [ ! -f "$SPOOL/test/group1/.config" ] || [ ! -f "$SPOOL/test/group1/.info" ]; then
  echo "Creating test groups (run test-scripts/create-test-groups.sh first or creating minimal group)..."
  mkdir -m 755 -p "$SPOOL/test/group1" "$SPOOL/test/group2"
  for name in "test.group1" "test.group2"; do
    dir="$SPOOL/${name//./\/}"
    cat > "$dir/.config" << EOF
description $name test group
creator     -
postlimit   1000
ccpost      -
replyto     -
voidemail   root
EOF
    printf 'start       0\nend         0\ntotal       0\n' > "$dir/.info"
  done
fi

# Sample email (RFC 822 style: headers, blank line, body)
# gonewsd prepends Newsgroups: and X-Mail-To-News-Gateway: then reads stdin
EMAIL=$(mktemp)
trap 'rm -f "$EMAIL"' EXIT
cat > "$EMAIL" << 'EOF'
From: sender@example.com
Subject: Test message from mail gateway

This is the body of the email.
It can have multiple lines.

Same as: cat email_message | gonewsd mailgateway rush.general
EOF

echo "Injecting email into group: $GROUP (config: $CONF)"
echo "Command: cat <email> | $BINDIR/gonewsd -c $CONF mailgateway $GROUP"
if ! cat "$EMAIL" | "$BINDIR/gonewsd" -c "$CONF" mailgateway "$GROUP"; then
  echo "FAIL: gonewsd mailgateway exited with error"
  exit 1
fi

# Verify article was created
# Group test.group1 -> dir test/group1; article 1 -> 1 (or 1000/1 if MsgModDirs)
DIR="$SPOOL/${GROUP//./\/}"
INFO="$DIR/.info"
if [ ! -f "$INFO" ]; then
  echo "FAIL: $INFO not found"
  exit 1
fi
TOTAL=$(awk '/^total / { print $2 }' "$INFO")
if [ -z "$TOTAL" ] || [ "$TOTAL" -lt 1 ]; then
  echo "FAIL: expected total >= 1 in $INFO, got total=$TOTAL"
  exit 1
fi
# Article 1 is stored as $DIR/1 (default) or $DIR/1000/1 (MsgModDirs)
if [ -f "$DIR/1" ]; then
  echo "OK: Article 1 created at $DIR/1"
elif [ -f "$DIR/1000/1" ]; then
  echo "OK: Article 1 created at $DIR/1000/1"
else
  echo "FAIL: No article file found under $DIR (total=$TOTAL)"
  exit 1
fi

echo "Mail gateway test passed: email injected into $GROUP, article $TOTAL present."
