#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# This is a convenience script to build gonewsd using the taskfile.

set -e
cd "$(dirname "$0")"
task build
