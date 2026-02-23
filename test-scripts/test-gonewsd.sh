#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Test gonewsd NNTP server capabilities.
# Expects gonewsd to be running on NNTP_ADDR (default 127.0.0.1:1119)
# with a test user (test@test.com / testPass1!) and test.group1, test.group2.
#
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
export NNTP_ADDR="${NNTP_ADDR:-127.0.0.1:1119}"
HOST="${NNTP_ADDR%%:*}"
PORT="${NNTP_ADDR##*:}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'
FAILURES=0

pass() { echo -e "${GREEN}[PASS]${NC} $1"; }
fail() { echo -e "${RED}[FAIL]${NC} $1"; FAILURES=$((FAILURES+1)); }
info() { echo -e "${YELLOW}[TEST]${NC} $1"; }

if [[ ! -x "$BINDIR/nntpclient" ]]; then
  echo "Build test binaries first: task build-test" >&2
  exit 1
fi
if [[ ! -x "$BINDIR/authtestclient" ]]; then
  echo "Build test binaries first: task build-test" >&2
  exit 1
fi

nc_cmd() {
  echo -e "$1" | nc -q 2 $HOST $PORT 2>/dev/null | tr -d '\r' || true
}

auth_nc_cmd() {
  echo -e "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\n$1\r\nQUIT\r\n" \
    | nc -q 2 $HOST $PORT 2>/dev/null | tr -d '\r' || true
}

echo ""
echo "========================================"
echo "  gonewsd NNTP Server Test Suite"
echo "  Server: $NNTP_ADDR"
echo "========================================"
echo ""

# ============================
# Basic NNTP commands
# ============================

info "Test 1: Server greeting"
OUTPUT=$(nc_cmd "QUIT\r\n")
if echo "$OUTPUT" | grep -q "200"; then
  pass "Server responded with greeting"
else
  fail "No server greeting received"
fi

info "Test 2: HELP command"
OUTPUT=$(nc_cmd "HELP\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "100"; then
  pass "HELP returns help text"
else
  fail "HELP command failed"
fi

info "Test 3: DATE command"
OUTPUT=$(nc_cmd "DATE\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "111"; then
  pass "DATE returns server date"
else
  fail "DATE command failed"
fi

info "Test 4: MODE READER"
OUTPUT=$(nc_cmd "MODE READER\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "200\|201"; then
  pass "MODE READER accepted"
else
  fail "MODE READER failed"
fi

# ============================
# CAPABILITIES (RFC 3977)
# ============================

info "Test 5: CAPABILITIES"
OUTPUT=$(nc_cmd "CAPABILITIES\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "101" && echo "$OUTPUT" | grep -q "VERSION 2"; then
  pass "CAPABILITIES returns VERSION 2"
else
  fail "CAPABILITIES missing or wrong"
fi
if echo "$OUTPUT" | grep -q "READER"; then
  pass "CAPABILITIES advertises READER"
else
  fail "CAPABILITIES missing READER"
fi
if echo "$OUTPUT" | grep -q "OVER"; then
  pass "CAPABILITIES advertises OVER"
else
  fail "CAPABILITIES missing OVER"
fi
if echo "$OUTPUT" | grep -q "HDR"; then
  pass "CAPABILITIES advertises HDR"
else
  fail "CAPABILITIES missing HDR"
fi
if echo "$OUTPUT" | grep -q "COMPRESS DEFLATE"; then
  pass "CAPABILITIES advertises COMPRESS DEFLATE"
else
  fail "CAPABILITIES missing COMPRESS DEFLATE"
fi

# ============================
# Authentication
# ============================

info "Test 6: AUTHINFO USER/PASS"
OUTPUT=$(auth_nc_cmd "GROUP test.group1")
if echo "$OUTPUT" | grep -q "281"; then
  pass "Authentication succeeded"
else
  fail "Authentication failed"
fi
if echo "$OUTPUT" | grep -q "211"; then
  pass "GROUP test.group1 selected"
else
  fail "GROUP test.group1 failed"
fi

# ============================
# LIST variants
# ============================

info "Test 7: LIST (basic)"
OUTPUT=$(auth_nc_cmd "LIST")
if echo "$OUTPUT" | grep -q "215" && echo "$OUTPUT" | grep -q "test.group1"; then
  pass "LIST returns groups including test.group1"
else
  fail "LIST failed"
fi

info "Test 8: LIST ACTIVE with exact wildmat"
OUTPUT=$(auth_nc_cmd "LIST ACTIVE test.group1")
if echo "$OUTPUT" | grep -q "test.group1" && ! echo "$OUTPUT" | grep -q "test.group2"; then
  pass "LIST ACTIVE test.group1 matches only test.group1"
else
  fail "LIST ACTIVE exact wildmat failed"
fi

info "Test 9: LIST ACTIVE with glob wildmat"
OUTPUT=$(auth_nc_cmd "LIST ACTIVE test.*")
if echo "$OUTPUT" | grep -q "test.group1" && echo "$OUTPUT" | grep -q "test.group2"; then
  pass "LIST ACTIVE test.* matches both test groups"
else
  fail "LIST ACTIVE glob wildmat failed"
fi

info "Test 10: LIST ACTIVE with non-matching wildmat"
OUTPUT=$(auth_nc_cmd "LIST ACTIVE no.match.*")
MATCHED=$(echo "$OUTPUT" | sed -n '/^215/,/^\.$/p' | { grep -v "^215\|^\." || true; } | wc -l)
if [[ "$MATCHED" -eq 0 ]]; then
  pass "LIST ACTIVE no.match.* returns no groups"
else
  fail "LIST ACTIVE non-matching wildmat returned $MATCHED groups"
fi

info "Test 11: LIST HEADERS"
OUTPUT=$(nc_cmd "LIST HEADERS\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "215"; then
  pass "LIST HEADERS returns header list"
else
  fail "LIST HEADERS failed"
fi

# ============================
# POST articles for subsequent tests
# ============================

info "Test 12: POST two articles"
OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nPOST\r\nFrom: test@test.com\r\nNewsgroups: test.group1\r\nSubject: Test article one\r\n\r\nBody of article 1\r\n.\r\nPOST\r\nFrom: test@test.com\r\nNewsgroups: test.group1\r\nSubject: Test article two\r\n\r\nBody of article 2\r\n.\r\nQUIT\r\n")
COUNT=$(echo "$OUTPUT" | grep -c "240" || true)
if [[ "$COUNT" -ge 2 ]]; then
  pass "Posted 2 articles to test.group1"
else
  fail "Expected 2 posts (240 responses), got $COUNT"
fi

# ============================
# NEXT and LAST
# ============================

info "Test 13: NEXT command"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 1\r\nNEXT")
if echo "$OUTPUT" | grep -q "223.*2"; then
  pass "NEXT moved to article 2"
else
  fail "NEXT failed"
fi

info "Test 14: LAST command"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 2\r\nLAST")
if echo "$OUTPUT" | grep -q "223.*1"; then
  pass "LAST moved back to article 1"
else
  fail "LAST failed"
fi

# ============================
# OVER / XOVER (RFC 3977)
# ============================

info "Test 15: OVER (all articles in group)"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nOVER")
if echo "$OUTPUT" | grep -q "224" && echo "$OUTPUT" | grep -q "Test article one"; then
  pass "OVER returns overview data"
else
  fail "OVER failed"
fi

info "Test 16: XOVER with range 1-1"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXOVER 1-1")
if echo "$OUTPUT" | grep -q "224" && echo "$OUTPUT" | grep -q "Test article one" && ! echo "$OUTPUT" | grep -q "Test article two"; then
  pass "XOVER 1-1 returns only article 1"
else
  fail "XOVER 1-1 failed"
fi

info "Test 17: XOVER with range 2-2"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXOVER 2-2")
if echo "$OUTPUT" | grep -q "224" && echo "$OUTPUT" | grep -q "Test article two"; then
  pass "XOVER 2-2 returns article 2"
else
  fail "XOVER 2-2 failed"
fi

# ============================
# XHDR / HDR (RFC 3977)
# ============================

info "Test 18: XHDR Subject 1-2"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXHDR Subject 1-2")
if echo "$OUTPUT" | grep -q "225" && echo "$OUTPUT" | grep -q "1 Test article one" && echo "$OUTPUT" | grep -q "2 Test article two"; then
  pass "XHDR Subject returns headers for both articles"
else
  fail "XHDR Subject failed"
fi

info "Test 19: HDR From 1-2"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nHDR From 1-2")
if echo "$OUTPUT" | grep -q "225" && echo "$OUTPUT" | grep -q "1 test@test.com"; then
  pass "HDR From returns correct values"
else
  fail "HDR From failed"
fi

info "Test 20: XHDR with single article"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXHDR Subject 1")
if echo "$OUTPUT" | grep -q "225" && echo "$OUTPUT" | grep -q "1 Test article one"; then
  pass "XHDR single article works"
else
  fail "XHDR single article failed"
fi

# ============================
# LISTGROUP with range
# ============================

info "Test 21: LISTGROUP with range 1-1"
OUTPUT=$(auth_nc_cmd "LISTGROUP test.group1 1-1")
if echo "$OUTPUT" | grep -q "^1$" && ! echo "$OUTPUT" | grep -q "^2$"; then
  pass "LISTGROUP range 1-1 returns only article 1"
else
  fail "LISTGROUP range 1-1 failed"
fi

info "Test 22: LISTGROUP with range 2-2"
OUTPUT=$(auth_nc_cmd "LISTGROUP test.group1 2-2")
if echo "$OUTPUT" | grep -q "^2$" && ! echo "$OUTPUT" | grep -q "^1$"; then
  pass "LISTGROUP range 2-2 returns only article 2"
else
  fail "LISTGROUP range 2-2 failed"
fi

# ============================
# NEWGROUPS with date filter
# ============================

info "Test 23: NEWGROUPS with past date (should return groups)"
OUTPUT=$(nc_cmd "NEWGROUPS 200101 000000\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "231" && echo "$OUTPUT" | grep -q "test.group1"; then
  pass "NEWGROUPS past date returns test groups"
else
  fail "NEWGROUPS past date failed"
fi

info "Test 24: NEWGROUPS with future date (should return no groups)"
OUTPUT=$(nc_cmd "NEWGROUPS 20990101 000000\r\nQUIT\r\n")
GRPCNT=$(echo "$OUTPUT" | sed -n '/^231/,/^\.$/p' | { grep -v "^231\|^\." || true; } | wc -l)
if [[ "$GRPCNT" -eq 0 ]]; then
  pass "NEWGROUPS future date returns no groups"
else
  fail "NEWGROUPS future date returned $GRPCNT groups (expected 0)"
fi

# ============================
# NEWNEWS
# ============================

info "Test 25: NEWNEWS with past date (should return article IDs)"
OUTPUT=$(auth_nc_cmd "NEWNEWS * 200101 000000")
if echo "$OUTPUT" | grep -q "230" && echo "$OUTPUT" | grep -q "<"; then
  pass "NEWNEWS returns article message-IDs"
else
  fail "NEWNEWS failed"
fi

info "Test 26: NEWNEWS with future date (should return nothing)"
OUTPUT=$(auth_nc_cmd "NEWNEWS * 20990101 000000")
ARTICLES=$(echo "$OUTPUT" | sed -n '/^230/,/^\.$/p' | { grep -v "^230\|^\." || true; } | wc -l)
if [[ "$ARTICLES" -eq 0 ]]; then
  pass "NEWNEWS future date returns no articles"
else
  fail "NEWNEWS future date returned $ARTICLES articles (expected 0)"
fi

info "Test 27: NEWNEWS with wildmat filter"
OUTPUT=$(auth_nc_cmd "NEWNEWS test.group1 200101 000000")
if echo "$OUTPUT" | grep -q "230" && echo "$OUTPUT" | grep -q "<"; then
  pass "NEWNEWS with group wildmat returns results"
else
  fail "NEWNEWS with group wildmat failed"
fi

# ============================
# CANCEL via control message
# ============================

info "Test 28: Retrieve Message-ID of article 2 for cancel test"
MSGID=$(auth_nc_cmd "GROUP test.group1\r\nXHDR Message-ID 2" | grep "^2 " | awk '{print $2}')
if [[ -n "$MSGID" ]]; then
  pass "Got Message-ID for article 2: $MSGID"
else
  fail "Could not retrieve Message-ID for article 2"
fi

if [[ -n "$MSGID" ]]; then
  info "Test 29: CANCEL article 2 (From matches)"
  OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nPOST\r\nFrom: test@test.com\r\nNewsgroups: test.group1\r\nSubject: cancel\r\nControl: cancel $MSGID\r\n\r\nCancel article 2\r\n.\r\nQUIT\r\n")
  if echo "$OUTPUT" | grep -q "240.*cancelled"; then
    pass "CANCEL accepted -- article deleted"
  else
    fail "CANCEL valid request failed"
  fi

  info "Test 30: Verify cancelled article is gone"
  OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 2")
  if echo "$OUTPUT" | grep -q "423\|430"; then
    pass "Article 2 no longer exists after cancel"
  else
    fail "Article 2 still exists after cancel"
  fi
fi

info "Test 31: CANCEL with wrong From (should be rejected)"
MSGID1=$(auth_nc_cmd "GROUP test.group1\r\nXHDR Message-ID 1" | grep "^1 " | awk '{print $2}')
if [[ -n "$MSGID1" ]]; then
  OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nPOST\r\nFrom: hacker@evil.com\r\nNewsgroups: test.group1\r\nSubject: cancel\r\nControl: cancel $MSGID1\r\n\r\nTrying to cancel someone else's article\r\n.\r\nQUIT\r\n")
  if echo "$OUTPUT" | grep -q "441.*does not match"; then
    pass "CANCEL rejected -- From mismatch"
  else
    fail "CANCEL should have been rejected for From mismatch"
  fi

  info "Test 32: Verify article 1 survives rejected cancel"
  OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 1")
  if echo "$OUTPUT" | grep -q "223"; then
    pass "Article 1 still exists after rejected cancel"
  else
    fail "Article 1 is missing (should have survived)"
  fi
else
  fail "Could not retrieve Message-ID for article 1 for cancel test"
fi

# ============================
# Edge cases
# ============================

info "Test 33: LAST at beginning of group (should fail)"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 1\r\nLAST")
if echo "$OUTPUT" | grep -q "422"; then
  pass "LAST at first article returns 422"
else
  fail "LAST at first article did not return 422"
fi

info "Test 34: NEXT at end of group (should fail)"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nNEXT")
if echo "$OUTPUT" | grep -q "421"; then
  pass "NEXT at last article returns 421"
else
  fail "NEXT at last article did not return 421"
fi

info "Test 35: GROUP for non-existent group"
OUTPUT=$(auth_nc_cmd "GROUP no.such.group")
if echo "$OUTPUT" | grep -q "411"; then
  pass "GROUP no.such.group returns 411"
else
  fail "GROUP non-existent group did not return 411"
fi

info "Test 36: STAT for non-existent article"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nSTAT 99999")
if echo "$OUTPUT" | grep -q "423"; then
  pass "STAT 99999 returns 423"
else
  fail "STAT non-existent article did not return 423"
fi

info "Test 37: XHDR with no group selected"
OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nXHDR Subject 1\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "412"; then
  pass "XHDR without group returns 412"
else
  fail "XHDR without group did not return 412"
fi

info "Test 38: OVER with no group selected"
OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nOVER\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "412"; then
  pass "OVER without group returns 412"
else
  fail "OVER without group did not return 412"
fi

info "Test 39: Bad auth credentials"
OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS wrongPassword\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "482\|452"; then
  pass "Wrong password rejected"
else
  fail "Wrong password was not rejected"
fi

# ============================
# XPAT (RFC 2980)
# ============================

info "Test 40: XPAT Subject range with matching pattern"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXPAT Subject 1-1 *one*")
if echo "$OUTPUT" | grep -q "221" && echo "$OUTPUT" | grep -q "1 Test article one"; then
  pass "XPAT returns matching article"
else
  fail "XPAT matching pattern failed"
fi

info "Test 41: XPAT Subject range with non-matching pattern"
OUTPUT=$(auth_nc_cmd "GROUP test.group1\r\nXPAT Subject 1-1 *zzz*")
XPAT_LINES=$(echo "$OUTPUT" | sed -n '/^221/,/^\.$/p' | { grep -v "^221\|^\." || true; } | wc -l)
if echo "$OUTPUT" | grep -q "221" && [[ "$XPAT_LINES" -eq 0 ]]; then
  pass "XPAT non-matching pattern returns no articles"
else
  fail "XPAT non-matching pattern returned $XPAT_LINES articles"
fi

info "Test 42: XPAT with no group selected"
OUTPUT=$(nc_cmd "AUTHINFO USER test@test.com\r\nAUTHINFO PASS testPass1!\r\nXPAT Subject 1 *test*\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "412"; then
  pass "XPAT without group returns 412"
else
  fail "XPAT without group did not return 412"
fi

# ============================
# LIST COUNTS (RFC 3977)
# ============================

info "Test 43: LIST COUNTS"
OUTPUT=$(auth_nc_cmd "LIST COUNTS")
if echo "$OUTPUT" | grep -q "215" && echo "$OUTPUT" | grep -q "test.group1"; then
  pass "LIST COUNTS returns group data"
else
  fail "LIST COUNTS failed"
fi

# ============================
# LIST DISTRIBUTIONS / DISTRIB.PATS
# ============================

info "Test 44: LIST DISTRIBUTIONS"
OUTPUT=$(nc_cmd "LIST DISTRIBUTIONS\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "215"; then
  pass "LIST DISTRIBUTIONS returns valid response"
else
  fail "LIST DISTRIBUTIONS failed"
fi

info "Test 45: LIST DISTRIB.PATS"
OUTPUT=$(nc_cmd "LIST DISTRIB.PATS\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "215"; then
  pass "LIST DISTRIB.PATS returns valid response"
else
  fail "LIST DISTRIB.PATS failed"
fi

# ============================
# AUTHINFO GENERIC (deprecated)
# ============================

info "Test 46: AUTHINFO GENERIC returns deprecation notice"
OUTPUT=$(nc_cmd "AUTHINFO GENERIC PLAIN\r\nQUIT\r\n")
if echo "$OUTPUT" | grep -q "501" && echo "$OUTPUT" | grep -qi "deprecated"; then
  pass "AUTHINFO GENERIC returns 501 deprecated"
else
  fail "AUTHINFO GENERIC did not return expected response"
fi

# ============================
# Summary
# ============================

echo ""
echo "========================================"
if [[ "$FAILURES" -eq 0 ]]; then
  echo -e "  ${GREEN}All tests passed!${NC}"
else
  echo -e "  ${RED}$FAILURES test(s) failed!${NC}"
fi
echo "========================================"
echo ""
exit $FAILURES
