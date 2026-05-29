#!/usr/bin/env bash
# confidentiality-tree-check.sh — ADR-0008 confidentiality hook.
#
# Per ADR-0008, the concrete confidentiality check — and anything it needs to
# know — lives outside this repository and is supplied by the operator or CI
# through the SUMPTER_CONFIDENTIALITY_CHECK environment variable. This hook runs
# that check when one is configured; otherwise it is a no-op, so the gate stays
# green for contributors who have not configured a local check.
#
# See docs/architecture/adr/0008-sensitive-data-outside-repository-trees.md

set -euo pipefail

checker="${SUMPTER_CONFIDENTIALITY_CHECK:-}"

if [ -z "$checker" ]; then
	echo "confidentiality hook: no operator-configured check; skipping (see ADR-0008)."
	exit 0
fi

if [ ! -x "$checker" ]; then
	echo "confidentiality hook: SUMPTER_CONFIDENTIALITY_CHECK is set but not executable" >&2
	exit 1
fi

# Per ADR-0008 the configured check lives OUTSIDE this repository. Canonicalize
# the checker (following symlinks to the real target) and the repo root, then
# refuse a checker that resolves inside the tree (which would reintroduce
# enforcement mechanics into the public surface) or that points back at this
# hook (which would recurse). Canonicalizing the target — not just its parent —
# is what closes the out-of-tree-symlink-to-in-repo-file bypass.
canonicalize() {
	if command -v realpath >/dev/null 2>&1; then
		realpath "$1" 2>/dev/null
	elif command -v python3 >/dev/null 2>&1; then
		python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$1" 2>/dev/null
	else
		# Last-resort fallback: resolve the parent dir only (no symlink follow on
		# the leaf). Weaker, but better than an unresolved relative path.
		(cd "$(dirname "$1")" 2>/dev/null && printf '%s/%s\n' "$(pwd -P)" "$(basename "$1")")
	fi
}

repo_root="$(canonicalize "$(dirname "${BASH_SOURCE[0]}")/..")"
this_hook="$(canonicalize "${BASH_SOURCE[0]}")"
checker_abs="$(canonicalize "$checker")"

if [ -z "$checker_abs" ]; then
	echo "confidentiality hook: cannot resolve SUMPTER_CONFIDENTIALITY_CHECK path" >&2
	exit 1
fi

if [ "$checker_abs" = "$this_hook" ]; then
	echo "confidentiality hook: SUMPTER_CONFIDENTIALITY_CHECK must not point at this hook (would recurse)" >&2
	exit 1
fi

case "$checker_abs" in
"$repo_root"/*)
	echo "confidentiality hook: SUMPTER_CONFIDENTIALITY_CHECK resolves inside the repo tree; ADR-0008 requires it to live outside the repository" >&2
	exit 1
	;;
esac

exec "$checker_abs"
