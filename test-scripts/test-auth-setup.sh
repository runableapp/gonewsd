#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Set up auth test environment: config with auth.db, users, and groups with different ACLs.
# Creates: auth config, SQLite DB, users (alice, bob, admin), groups (test.public, test.private, test.group1, test.group2).
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="${BINDIR:-$ROOT/bin}"
cd "$ROOT"

TESTDIR="${TESTDIR:-$ROOT/test-scripts/auth-test}"
SPOOL="$TESTDIR/spool"
DB="$TESTDIR/auth.db"
CONF="$TESTDIR/auth-test.conf"
export TESTDIR SPOOL DB CONF

if [[ ! -x "$BINDIR/gonewsd" ]]; then
  echo "Build gonewsd first: task build  (or: go build -o bin/gonewsd ./cmd/gonewsd)" >&2
  exit 1
fi

echo "Auth test dir: $TESTDIR"
mkdir -p "$SPOOL"

# Config: auth.mode=private so unauthenticated has no default access; ACLs override per group.
# Use port 1120 to avoid conflict with main test server on 1119.
cat > "$CONF" << EOF
ErrorLog stderr
Listen :1120
SpoolDir $SPOOL
auth.db $DB
auth.mode private
auth.log $TESTDIR/auth.log
pidfile $TESTDIR/gonewsd.pid
User nobody
EOF

# Remove stale DB so we start clean
rm -f "$DB"

# Groups must exist before users can be assigned to them. Create groups with different ACLs first.
# test.public: world rw -> everyone can read and post without auth
"$BINDIR/gonewsd" -c "$CONF" addgroup -group test.public -g rw -o rw \
  -desc "Public test group" -creator "test-setup" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
# test.private: world r only -> only group members can post; world can read
"$BINDIR/gonewsd" -c "$CONF" addgroup -group test.private -g rw -o r \
  -desc "Private test group" -creator "test-setup" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
# test.group1: same (for alice)
"$BINDIR/gonewsd" -c "$CONF" addgroup -group test.group1 -g rw -o r \
  -desc "Test group 1 for alice" -creator "test-setup" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
# test.group2: same (for bob)
"$BINDIR/gonewsd" -c "$CONF" addgroup -group test.group2 -g rw -o r \
  -desc "Test group 2 for bob" -creator "test-setup" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"

# Users (email + password 9–20 chars, no space)
# alice: access to test.group1 only
# bob: access to test.group2 only
# admin: access to all groups (*)
"$BINDIR/gonewsd" -c "$CONF" adduser -user alice@test.com -pass alicepass12 -realname "Alice Test" -groups test.group1
"$BINDIR/gonewsd" -c "$CONF" adduser -user bob@test.com -pass bobspass12 -realname "Bob Test" -groups test.group2
"$BINDIR/gonewsd" -c "$CONF" adduser -user admin@test.com -pass adminpass12 -realname "Admin" -groups '*'

# Post one article into test.public so we can read it (via mailgateway or we'll POST in test)
# Create a minimal article file so GROUP test.public and ARTICLE 1 work
mkdir -p "$SPOOL/test/public"
printf 'start       1\nend         1\ntotal       1\n' > "$SPOOL/test/public/.info"
printf 'description public\ncreator     -\npostlimit   1000\nccpost      -\nreplyto     -\nvoidemail   root\n' > "$SPOOL/test/public/.config"
printf 'From: setup@test\nNewsgroups: test.public\nSubject: first\nMessage-ID: <1@test>\nDate: Mon, 01 Jan 2025 00:00:00 +0000\n\nbody\n' > "$SPOOL/test/public/1"

echo "Setup done. Config: $CONF"
echo "  Users: alice@test.com (test.group1), bob@test.com (test.group2), admin@test.com (*)"
echo "  Groups: test.public (o=rw), test.private (o=r), test.group1 (o=r), test.group2 (o=r)"
