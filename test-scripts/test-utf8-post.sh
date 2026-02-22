#!/bin/bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Test UTF-8 posting to gonewsd.
# This script posts an article with Korean text and verifies it was stored correctly.
#
# Usage: ./test-utf8-post.sh [host:port] [group]
#   Default: 127.0.0.1:1119 test.public
#
# Requires: netcat (nc), AUTH_USER and AUTH_PASS env vars for authenticated posting

set -e

ADDR="${1:-127.0.0.1:1119}"
GROUP="${2:-test.public}"
HOST="${ADDR%:*}"
PORT="${ADDR#*:}"

echo "Testing UTF-8 posting to $ADDR group=$GROUP"
echo

# Korean test content
KOREAN_SUBJECT="한글 테스트 - UTF-8 Test"
KOREAN_BODY="안녕하세요! 이것은 한글 테스트입니다.
Hello! This is a Korean UTF-8 test.
日本語テスト (Japanese test too)"

# Create temporary file for the NNTP conversation
TMPFILE=$(mktemp)
trap "rm -f $TMPFILE" EXIT

# Build the article
MSGID="<utf8-test-$(date +%s)@test>"

cat > $TMPFILE << EOF
From: utf8test@test.local
Newsgroups: $GROUP
Subject: $KOREAN_SUBJECT
Message-ID: $MSGID

$KOREAN_BODY
EOF

echo "=== Article to post (should show Korean characters): ==="
cat $TMPFILE
echo
echo "=== Raw bytes of article body: ==="
echo "$KOREAN_BODY" | xxd | head -10
echo

# Function to do NNTP conversation
do_nntp() {
    # Use heredoc with netcat
    {
        # Wait for greeting
        sleep 0.2
        
        # MODE READER
        echo "MODE READER"
        sleep 0.1
        
        # Auth if credentials provided
        if [ -n "$AUTH_USER" ] && [ -n "$AUTH_PASS" ]; then
            echo "AUTHINFO USER $AUTH_USER"
            sleep 0.1
            echo "AUTHINFO PASS $AUTH_PASS"
            sleep 0.1
        fi
        
        # POST command
        echo "POST"
        sleep 0.1
        
        # Send article headers
        echo "From: utf8test@test.local"
        echo "Newsgroups: $GROUP"
        echo "Subject: $KOREAN_SUBJECT"
        echo "Message-ID: $MSGID"
        echo ""
        
        # Send body with Korean
        echo "$KOREAN_BODY"
        
        # End of article
        echo "."
        sleep 0.2
        
        # Read the article back
        echo "GROUP $GROUP"
        sleep 0.1
        echo "ARTICLE $MSGID"
        sleep 0.3
        
        echo "QUIT"
    } | nc -q 2 $HOST $PORT
}

echo "=== NNTP conversation: ==="
RESULT=$(do_nntp 2>&1)
echo "$RESULT"
echo

# Check if post succeeded
if echo "$RESULT" | grep -q "240"; then
    echo "✓ POST succeeded (240 response)"
else
    echo "✗ POST may have failed - check response above"
fi

# Check if article was retrieved
if echo "$RESULT" | grep -q "220"; then
    echo "✓ ARTICLE retrieved (220 response)"
    
    # Check if Korean text is in the response
    if echo "$RESULT" | grep -q "한글"; then
        echo "✓ Korean text preserved correctly!"
    elif echo "$RESULT" | grep -q "???"; then
        echo "✗ Korean text was converted to '???' - UTF-8 NOT working"
    else
        echo "? Could not verify Korean text in response"
    fi
else
    echo "? Could not retrieve article - check response above"
fi

echo
echo "=== Done ==="
