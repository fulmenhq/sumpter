#!/bin/sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
"$SCRIPT_DIR/run-positive.sh"
"$SCRIPT_DIR/run-negative.sh"
