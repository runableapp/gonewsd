#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Build and run all automated tests.
# This is a convenience wrapper for: task test
#
set -e
cd "$(dirname "$0")"
exec task test
