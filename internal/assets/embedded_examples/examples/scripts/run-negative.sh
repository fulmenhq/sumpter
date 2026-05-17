#!/bin/sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXAMPLES_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

for case_dir in "$EXAMPLES_DIR"/cases/9[0-9]-*/; do
	[ -d "$case_dir" ] || continue
	"$SCRIPT_DIR/run-case.sh" "$case_dir"
done
