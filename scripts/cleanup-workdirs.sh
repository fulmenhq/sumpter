#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: cleanup-workdirs.sh [--root DIR] [--pattern GLOB] [--max-age-hours H] [--dry-run]

Safely remove stale Sumpter workdirs (e.g., large uncompressed staging areas) that are older than the requested age.

Options:
  --root DIR          Root directory to scan (default: $SUMPTER_WORKDIR, else $TMPDIR or /tmp)
  --pattern GLOB      Directory name glob to match (default: sumpter-*)
  --max-age-hours H   Minimum age before deletion (default: 24)
  --dry-run           List matches without deleting
  -h, --help          Show this help
EOF
}

root="${SUMPTER_WORKDIR:-${TMPDIR:-/tmp}}"
pattern="sumpter-*"
max_age_hours=24
dry_run=false

while [[ $# -gt 0 ]]; do
	case "$1" in
	--root)
		root="${2:-}"
		shift 2
		;;
	--pattern)
		pattern="${2:-}"
		shift 2
		;;
	--max-age-hours)
		max_age_hours="${2:-}"
		shift 2
		;;
	--dry-run)
		dry_run=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Unknown option: $1" >&2
		usage
		exit 1
		;;
	esac
done

if [[ -z "$root" || ! -d "$root" ]]; then
	echo "Root directory not found: $root" >&2
	exit 1
fi

if ! [[ "$max_age_hours" =~ ^[0-9]+$ ]]; then
	echo "max-age-hours must be an integer (hours)" >&2
	exit 1
fi

max_age_mins=$((max_age_hours * 60))

echo "Scanning '$root' for directories matching '$pattern' older than ${max_age_hours}h (dry-run=$dry_run)"

found=0
removed=0

while IFS= read -r dir; do
	((found++))
	markers=""
	[[ -f "$dir/.ready" ]] && markers="$markers [ready]"
	[[ -f "$dir/.meta.json" ]] && markers="$markers [meta]"

	if $dry_run; then
		echo "DRY-RUN would remove: $dir$markers"
		continue
	fi

	rm -rf "$dir"
	((removed++))
	echo "Removed: $dir$markers"
done < <(find "$root" -maxdepth 1 -type d -name "$pattern" -mmin +"$max_age_mins" 2>/dev/null)

echo "Done. Found: $found, Removed: $removed"
