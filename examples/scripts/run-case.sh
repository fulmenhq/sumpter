#!/bin/sh
set -eu

# Usage: run-case.sh <case-dir> [--variant xml|json|ndjson]
#
# Without --variant the historical default applies: input.xml, else input.json,
# with recipe/ and expected/ at the case root. With --variant the run uses
# variants/<fmt>/{input.<fmt>,recipe/,expected/} and never falls back.
CASE_DIR="${1:?case folder required}"
shift
VARIANT=""
HAS_VARIANT=0
while [ "$#" -gt 0 ]; do
	case "$1" in
	--variant)
		[ "$HAS_VARIANT" -eq 0 ] || {
			echo "--variant given more than once" >&2
			exit 2
		}
		[ "$#" -ge 2 ] && [ -n "$2" ] || {
			echo "--variant requires a value" >&2
			exit 2
		}
		VARIANT="$2"
		HAS_VARIANT=1
		shift 2
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
done
CASE_DIR="$(cd "$CASE_DIR" && pwd)"
CASE_NAME="$(basename "$CASE_DIR")"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXAMPLES_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$EXAMPLES_DIR/.." && pwd)"
SUMPTER_BIN="${SUMPTER_BIN:-$REPO_ROOT/dist/sumpter}"

if [ ! -x "$SUMPTER_BIN" ]; then
	echo "SUMPTER_BIN is not executable: $SUMPTER_BIN" >&2
	echo "Run 'make build' or set SUMPTER_BIN to a sumpter binary." >&2
	exit 2
fi

if [ "$HAS_VARIANT" -eq 1 ]; then
	case "$VARIANT" in
	xml | json | ndjson) ;;
	*)
		echo "FAIL [$CASE_NAME]: unknown variant: $VARIANT" >&2
		exit 2
		;;
	esac
	RUN_DIR="$CASE_DIR/variants/$VARIANT"
	LABEL="$CASE_NAME:$VARIANT"
	INPUT_FILE="$RUN_DIR/input.$VARIANT"
	if [ ! -f "$INPUT_FILE" ] || [ ! -f "$RUN_DIR/recipe/recipe.yaml" ]; then
		echo "FAIL [$LABEL]: variant not found: $RUN_DIR" >&2
		exit 2
	fi
else
	RUN_DIR="$CASE_DIR"
	LABEL="$CASE_NAME"
	INPUT_FILE="$CASE_DIR/input.xml"
	if [ ! -f "$INPUT_FILE" ] && [ -f "$CASE_DIR/input.json" ]; then
		INPUT_FILE="$CASE_DIR/input.json"
	fi
	if [ ! -f "$INPUT_FILE" ] || [ ! -f "$CASE_DIR/recipe/recipe.yaml" ]; then
		echo "FAIL [$LABEL]: no default run (variant-only case); pass --variant" >&2
		exit 2
	fi
fi
EXPECTED_DIR="$RUN_DIR/expected"

OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sumpter-example-${CASE_NAME}.XXXXXX")"
trap 'rm -rf "$OUT_DIR"' EXIT

OUTPUT_PATTERN="records.jsonl"
RUN_ID="0196d5b2-0d00-7c00-8000-000000000006"

case "$CASE_NAME" in
9[0-9]-*)
	# Negatives run with the manifest on: a refused run must publish nothing.
	NEG_OUT="$OUT_DIR/out"
	set +e
	OUTPUT="$("$SUMPTER_BIN" recipes run extract "$RUN_DIR/recipe" \
		--files "$INPUT_FILE" \
		--output-path "$NEG_OUT" \
		--output-pattern "$OUTPUT_PATTERN" \
		--run-id "$RUN_ID" 2>&1)"
	EXIT_CODE=$?
	set -e

	if [ "$EXIT_CODE" -eq 0 ]; then
		echo "FAIL [$LABEL]: expected non-zero exit, got 0" >&2
		exit 1
	fi

	EXPECTED_ERROR="$(cat "$EXPECTED_DIR/error.txt")"
	if ! printf '%s\n' "$OUTPUT" | grep -qF "$EXPECTED_ERROR"; then
		echo "FAIL [$LABEL]: expected error substring not found" >&2
		echo "expected: $EXPECTED_ERROR" >&2
		echo "actual:" >&2
		echo "$OUTPUT" >&2
		exit 1
	fi

	if [ -d "$NEG_OUT" ]; then
		PUBLISHED="$(find "$NEG_OUT" -type f \( -name 'records*.jsonl' -o -name manifest.json -o -name failures.json -o -name dispositions.json \) -print)"
		if [ -n "$PUBLISHED" ]; then
			echo "FAIL [$LABEL]: refused run published output:" >&2
			echo "$PUBLISHED" >&2
			exit 1
		fi
	fi

	echo "PASS [$LABEL] (negative)"
	exit 0
	;;
esac

"$SUMPTER_BIN" recipes run extract "$RUN_DIR/recipe" \
	--files "$INPUT_FILE" \
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
	echo "FAIL [$LABEL]: stable output diff" >&2
	exit 1
fi

echo "PASS [$LABEL]"
