#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Run auth tests: setup users/groups, start server, run authtestclient scenarios, assert results.
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
export BINDIR
cd "$ROOT"

# Build binaries into bin/ if needed
if [[ ! -x "$BINDIR/gonewsd" ]] || [[ ! -x "$BINDIR/authtestclient" ]]; then
  task build 2>/dev/null || { mkdir -p bin; go build -o bin/gonewsd ./cmd/gonewsd; go build -o bin/authtestclient ./cmd/authtestclient; }
fi

# Setup: config, users, groups with different ACLs
./test-scripts/test-auth-setup.sh
CONF="${TESTDIR:-$ROOT/test-scripts/auth-test}/auth-test.conf"
export NNTP_ADDR="127.0.0.1:1120"

# Start server in background (no fork so we can kill it)
"$BINDIR/gonewsd" -d -c "$CONF" &
SRVPID=$!
trap "kill $SRVPID 2>/dev/null || true" EXIT

# Wait for server to listen
sleep 2

echo "=== 1. Unauthenticated: LIST, GROUP test.public, ARTICLE 1, POST test.public (world rw) ==="
OUT=$("$BINDIR/authtestclient" test.public test.public 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "215" || { echo "Expected 215 LIST ACTIVE" >&2; exit 1; }
echo "$OUT" | grep -q "211" || { echo "Expected 211 GROUP" >&2; exit 1; }
echo "$OUT" | grep -q "240" || { echo "Expected 240 POST success (test.public o=rw)" >&2; exit 1; }
echo "OK: unauthenticated can read and post to test.public"

echo ""
echo "=== 2. Unauthenticated: POST test.group1 (world r only) -> expect 480 ==="
OUT=$("$BINDIR/authtestclient" test.group1 test.group1 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "480" || { echo "Expected 480 auth required for POST to test.group1" >&2; exit 1; }
echo "OK: unauthenticated POST to test.group1 denied"

echo ""
echo "=== 3. Alice (test.group1): LIST, GROUP test.group1, POST test.group1 -> 240 ==="
export AUTH_USER=alice@test.com AUTH_PASS=alicepass12
OUT=$("$BINDIR/authtestclient" test.group1 test.group1 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "281\|250" || { echo "Expected 281/250 auth OK" >&2; exit 1; }
echo "$OUT" | grep -q "211" || { echo "Expected 211 GROUP test.group1" >&2; exit 1; }
echo "$OUT" | grep -q "240" || { echo "Expected 240 POST success" >&2; exit 1; }
unset AUTH_USER AUTH_PASS
echo "OK: alice can read and post to test.group1"

echo ""
echo "=== 4. Bob (test.group2): GROUP test.group2, POST test.group2 -> 240 ==="
export AUTH_USER=bob@test.com AUTH_PASS=bobspass12
OUT=$("$BINDIR/authtestclient" test.group2 test.group2 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "240" || { echo "Expected 240 POST success" >&2; exit 1; }
unset AUTH_USER AUTH_PASS
echo "OK: bob can read and post to test.group2"

echo ""
echo "=== 5. Alice POST test.group2 (not in group) -> 480 ==="
export AUTH_USER=alice@test.com AUTH_PASS=alicepass12
OUT=$("$BINDIR/authtestclient" test.group2 test.group2 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "480" || { echo "Expected 480 POST denied (alice not in test.group2)" >&2; exit 1; }
unset AUTH_USER AUTH_PASS
echo "OK: alice cannot post to test.group2"

echo ""
echo "=== 6. Admin (*): GROUP test.private, POST test.private -> 240 ==="
export AUTH_USER=admin@test.com AUTH_PASS=adminpass12
OUT=$("$BINDIR/authtestclient" test.private test.private 2>&1)
echo "$OUT"
echo "$OUT" | grep -q "240" || { echo "Expected 240 POST success (admin has all groups)" >&2; exit 1; }
unset AUTH_USER AUTH_PASS
echo "OK: admin can post to test.private"

echo ""
echo "=== 7. Wrong password -> auth failure ==="
export AUTH_USER=alice@test.com AUTH_PASS=wrongpass
OUT=$("$BINDIR/authtestclient" test.group1 2>&1) || true
echo "$OUT"
echo "$OUT" | grep -q "482\|452" || { echo "Expected 482/452 auth rejected" >&2; exit 1; }
unset AUTH_USER AUTH_PASS
echo "OK: wrong password rejected"

echo ""
echo "All auth tests passed."
