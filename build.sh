#!/usr/bin/env bash
set -euo pipefail

if ! command -v task >/dev/null 2>&1; then
  echo "Error: 'task' command not found. Install go-task first: https://taskfile.dev/installation/" >&2
  exit 1
fi

task build-all
#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# This is a convenience script to build gonewsd using the taskfile.

set -e
cd "$(dirname "$0")"
task build
