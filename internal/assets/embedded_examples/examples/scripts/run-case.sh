#!/bin/sh
set -eu

CASE_DIR="${1:?case folder required}"
CASE_DIR="$(cd "$CASE_DIR" && pwd)"
CASE_NAME="$(basename "$CASE_DIR")"
EXPECTED_DIR="$CASE_DIR/expected"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXAMPLES_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$EXAMPLES_DIR/.." && pwd)"
SUMPTER_BIN="${SUMPTER_BIN:-$REPO_ROOT/dist/sumpter}"

if [ ! -x "$SUMPTER_BIN" ]; then
	echo "SUMPTER_BIN is not executable: $SUMPTER_BIN" >&2
	echo "Run 'make build' or set SUMPTER_BIN to a sumpter binary." >&2
	exit 2
fi

OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sumpter-example-${CASE_NAME}.XXXXXX")"
trap 'rm -rf "$OUT_DIR"' EXIT

OUTPUT_PATTERN="records.jsonl"
RUN_ID="0196d5b2-0d00-7c00-8000-000000000006"

case "$CASE_NAME" in
9[0-9]-*)
	set +e
	OUTPUT="$("$SUMPTER_BIN" recipes run extract "$CASE_DIR/recipe" \
		--files "$CASE_DIR/input.xml" \
		--output-path "$OUT_DIR" \
		--output-pattern "$OUTPUT_PATTERN" \
		--run-id "$RUN_ID" \
		--no-manifest 2>&1)"
	EXIT_CODE=$?
	set -e

	if [ "$EXIT_CODE" -eq 0 ]; then
		echo "FAIL [$CASE_NAME]: expected non-zero exit, got 0" >&2
		exit 1
	fi

	EXPECTED_ERROR="$(cat "$EXPECTED_DIR/error.txt")"
	if ! printf '%s\n' "$OUTPUT" | grep -qF "$EXPECTED_ERROR"; then
		echo "FAIL [$CASE_NAME]: expected error substring not found" >&2
		echo "expected: $EXPECTED_ERROR" >&2
		echo "actual:" >&2
		echo "$OUTPUT" >&2
		exit 1
	fi

	echo "PASS [$CASE_NAME] (negative)"
	exit 0
	;;
esac

"$SUMPTER_BIN" recipes run extract "$CASE_DIR/recipe" \
	--files "$CASE_DIR/input.xml" \
	--output-path "$OUT_DIR" \
	--output-pattern "$OUTPUT_PATTERN" \
	--run-id "$RUN_ID" \
	--no-manifest >/dev/null

ACTUAL_JSON="$OUT_DIR/actual.json"
(
	cd "$REPO_ROOT"
	export GOCACHE="${GOCACHE:-$REPO_ROOT/.cache/go-build}"
	export GOMODCACHE="${GOMODCACHE:-$REPO_ROOT/.cache/go-mod}"
	go run ./examples/internal/canonicalize "$OUT_DIR/$OUTPUT_PATTERN"
) >"$ACTUAL_JSON"

if ! diff -u "$EXPECTED_DIR/output.json" "$ACTUAL_JSON"; then
	echo "FAIL [$CASE_NAME]: stable output diff" >&2
	exit 1
fi

echo "PASS [$CASE_NAME]"
