#!/usr/bin/env bash
# confidentiality-tree-check.sh — ADR-0008 confidentiality hook.
#
# Per ADR-0008, the concrete confidentiality check — and anything it needs to
# know — lives outside this repository and is supplied by the operator or CI
# through the SUMPTER_CONFIDENTIALITY_CHECK environment variable. Optional mode
# permits unconfigured contributor checks; required mode does not.
#
# See docs/decisions/confidentiality-validation-boundaries.md

set -euo pipefail

fail() {
	echo "confidentiality hook: $1" >&2
	exit 1
}

# The '-' expansion defaults only an absent variable, not a set empty value.
required="${SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK-0}"
case "$required" in
0 | 1) ;;
*) fail "invalid SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK; expected 0 or 1" ;;
esac

checker="${SUMPTER_CONFIDENTIALITY_CHECK:-}"
if [ -z "$checker" ]; then
	if [ "$required" = 1 ]; then
		fail "required check is not configured"
	fi
	echo "confidentiality hook: SKIPPED (no configured check; not confidentiality clearance)"
	exit 0
fi

if [ ! -f "$checker" ] || [ ! -r "$checker" ] || [ ! -x "$checker" ]; then
	fail "configured check must be a readable executable file"
fi

# Fully resolve existing targets, including leaf symlinks, in either mode.
# Preserve resolver output's final newline using a sentinel, then reject any
# ambiguous/multiline result rather than truncating a pathname. No parent-only
# fallback or failed Git-discovery inference is used.
canonicalize() {
	local path result
	case "$1" in
	/*) path="$1" ;;
	*) path="$(pwd -P)/$1" ;;
	esac
	if command -v realpath >/dev/null 2>&1; then
		result="$(realpath "$path" 2>/dev/null && printf '\001')" || return 1
	elif command -v python3 >/dev/null 2>&1; then
		result="$(python3 -c 'from pathlib import Path; import sys; print(Path(sys.argv[1]).resolve(strict=True))' "$path" 2>/dev/null && printf '\001')" || return 1
	else
		return 1
	fi
	result="${result%$'\001'}"
	case "$result" in
	*$'\n') result="${result%$'\n'}" ;;
	*) return 1 ;;
	esac
	case "$result" in
	/*) ;;
	*) return 1 ;;
	esac
	case "$result" in
	*$'\n'* | *$'\r'* | *//* | */./* | */../* | */. | */..) return 1 ;;
	esac
	[ -e "$result" ] || return 1
	printf '%s' "$result"
}

# Resolve the hook first so invocation through a symlink uses its real checkout.
this_hook="$(canonicalize "${BASH_SOURCE[0]}")" || fail "cannot resolve hook path"
repo_root="$(canonicalize "${this_hook%/*}/..")" || fail "cannot resolve checkout root"
checker_abs="$(canonicalize "$checker")" || fail "cannot resolve configured check"

if [ ! -d "$repo_root" ] || [ ! -f "$this_hook" ]; then
	fail "invalid checkout root or hook"
fi
if [ ! -f "$checker_abs" ] || [ ! -r "$checker_abs" ] || [ ! -x "$checker_abs" ]; then
	fail "resolved check must be a readable executable file"
fi

if [ "$checker_abs" = "$this_hook" ]; then
	fail "configured check must not point at this hook (would recurse)"
fi

case "$checker_abs" in
"$repo_root" | "$repo_root"/*)
	fail "configured check must live outside this checkout (see ADR-0008)"
	;;
esac

exec "$checker_abs"
