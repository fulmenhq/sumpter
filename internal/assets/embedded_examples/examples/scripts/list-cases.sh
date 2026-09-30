#!/bin/sh
# Enumerate runnable example cases, one "case[:variant]" per line.
#
# A bare "case" is the historical default run of examples/cases/<case>.
# "case:<fmt>" is the explicit variant under examples/cases/<case>/variants/<fmt>.
# Fails (non-zero, no partial success) on an empty or malformed inventory.
#
# Usage: list-cases.sh [--positive|--negative]
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CASES_DIR="$(cd "$SCRIPT_DIR/../cases" && pwd)"

FILTER="${1:-all}"
case "$FILTER" in
all | --positive | --negative) ;;
*)
	echo "list-cases: unknown filter: $FILTER" >&2
	exit 2
	;;
esac

fail() {
	echo "list-cases: $*" >&2
	exit 1
}

is_negative() {
	case "$1" in
	9[0-9]-*) return 0 ;;
	*) return 1 ;;
	esac
}

want() {
	case "$FILTER" in
	--positive) ! is_negative "$1" ;;
	--negative) is_negative "$1" ;;
	*) return 0 ;;
	esac
}

check_expected() {
	# $1 = case name, $2 = dir holding expected/
	if is_negative "$1"; then
		[ -f "$2/expected/error.txt" ] || fail "missing expected/error.txt in $2"
	else
		[ -f "$2/expected/output.json" ] || fail "missing expected/output.json in $2"
	fi
}

OUT=""
COUNT=0
for case_dir in "$CASES_DIR"/*-*/; do
	[ -d "$case_dir" ] || continue
	case_dir="${case_dir%/}"
	name="$(basename "$case_dir")"

	[ -f "$case_dir/recipe/recipe.yaml" ] || fail "missing recipe/recipe.yaml in $case_dir"
	if [ ! -f "$case_dir/input.xml" ] && [ ! -f "$case_dir/input.json" ]; then
		fail "missing input.xml or input.json in $case_dir"
	fi
	check_expected "$name" "$case_dir"
	if want "$name"; then
		OUT="$OUT$name
"
		COUNT=$((COUNT + 1))
	fi

	[ -d "$case_dir/variants" ] || continue
	for variant_dir in "$case_dir"/variants/*/; do
		[ -d "$variant_dir" ] || continue
		variant_dir="${variant_dir%/}"
		fmt="$(basename "$variant_dir")"
		case "$fmt" in
		xml | json | ndjson) ;;
		*) fail "unknown variant '$fmt' in $case_dir" ;;
		esac
		[ -f "$variant_dir/input.$fmt" ] || fail "missing input.$fmt in $variant_dir"
		[ -f "$variant_dir/recipe/recipe.yaml" ] || fail "missing recipe/recipe.yaml in $variant_dir"
		check_expected "$name" "$variant_dir"
		if want "$name"; then
			OUT="$OUT$name:$fmt
"
			COUNT=$((COUNT + 1))
		fi
	done
done

[ "$COUNT" -gt 0 ] || fail "no example cases found for filter $FILTER"
printf '%s' "$OUT"
