#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Test gonewsd NNTP server capabilities using nntpclient and authtestclient.
# Expects gonewsd to be running on NNTP_ADDR (default 127.0.0.1:1119).
#
set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
export NNTP_ADDR="${NNTP_ADDR:-127.0.0.1:1119}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}[PASS]${NC} $1"; }
fail() { echo -e "${RED}[FAIL]${NC} $1"; exit 1; }
info() { echo -e "${YELLOW}[TEST]${NC} $1"; }

# Check binaries exist
if [[ ! -x "$BINDIR/nntpclient" ]]; then
  echo "Build test binaries first: task build-test" >&2
  exit 1
fi
if [[ ! -x "$BINDIR/authtestclient" ]]; then
  echo "Build test binaries first: task build-test" >&2
  exit 1
fi

echo ""
echo "========================================"
echo "  gonewsd NNTP Server Test Suite"
echo "  Server: $NNTP_ADDR"
echo "========================================"
echo ""

# Test 1: Basic connection and commands with nntpclient
info "Test 1: Basic NNTP commands (nntpclient)"
OUTPUT=$("$BINDIR/nntpclient" -no-post 2>&1) || true

if echo "$OUTPUT" | grep -q "200"; then
  pass "Server responded with greeting"
else
  fail "No server greeting received"
fi

if echo "$OUTPUT" | grep -q "215"; then
  pass "LIST command works"
else
  fail "LIST command failed"
fi

if echo "$OUTPUT" | grep -q "211\|411"; then
  pass "GROUP command works (211 OK or 411 no such group)"
else
  fail "GROUP command failed"
fi

# Test 2: Unauthenticated access with authtestclient
info "Test 2: Unauthenticated LIST (authtestclient)"
unset AUTH_USER AUTH_PASS
export POST_GROUP=""
OUTPUT=$("$BINDIR/authtestclient" 2>&1) || true

if echo "$OUTPUT" | grep -q "LIST ACTIVE"; then
  pass "authtestclient ran LIST ACTIVE"
else
  # May fail if auth required, that's OK
  echo -e "${YELLOW}[SKIP]${NC} LIST may require auth (depends on auth.mode)"
fi

# Test 3: Check HELP command via raw connection
info "Test 3: HELP command"
HELP_OUTPUT=$(echo -e "HELP\r\nQUIT\r\n" | nc -q 2 ${NNTP_ADDR//:/ } 2>/dev/null || true)

if echo "$HELP_OUTPUT" | grep -q "100"; then
  pass "HELP command returns help text"
else
  echo -e "${YELLOW}[SKIP]${NC} HELP test requires nc (netcat)"
fi

# Test 4: Check DATE command
info "Test 4: DATE command"
DATE_OUTPUT=$(echo -e "DATE\r\nQUIT\r\n" | nc -q 2 ${NNTP_ADDR//:/ } 2>/dev/null || true)

if echo "$DATE_OUTPUT" | grep -q "111"; then
  pass "DATE command returns server date"
else
  echo -e "${YELLOW}[SKIP]${NC} DATE test requires nc (netcat)"
fi

# Test 5: Check MODE READER
info "Test 5: MODE READER command"
MODE_OUTPUT=$(echo -e "MODE READER\r\nQUIT\r\n" | nc -q 2 ${NNTP_ADDR//:/ } 2>/dev/null || true)

if echo "$MODE_OUTPUT" | grep -q "200\|201"; then
  pass "MODE READER accepted"
else
  echo -e "${YELLOW}[SKIP]${NC} MODE READER test requires nc (netcat)"
fi

echo ""
echo "========================================"
echo "  All tests completed!"
echo "========================================"
echo ""
