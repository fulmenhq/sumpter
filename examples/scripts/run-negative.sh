#!/bin/sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXAMPLES_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Capture first so an enumerator failure fails the run.
CASES="$("$SCRIPT_DIR/list-cases.sh" --negative)"

for entry in $CASES; do
	case "$entry" in
	*:*) "$SCRIPT_DIR/run-case.sh" "$EXAMPLES_DIR/cases/${entry%%:*}" --variant "${entry#*:}" ;;
	*) "$SCRIPT_DIR/run-case.sh" "$EXAMPLES_DIR/cases/$entry" ;;
	esac
done
